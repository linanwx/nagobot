package thread

import (
	"context"
	"regexp"
	"strings"
)

// The <coder> field asks: is the user requesting code to be PRODUCED — written,
// debugged, refactored, or built into a script/program/web page? A true verdict
// hints the main model to dispatch the coder subagent, which is bound to a
// code-specialized model and keeps the tool spam of a coding loop out of the
// main session.
//
// The boundary that matters is production vs. explanation. "写个爬虫抓价格" is
// true; "什么是动态规划" and "explain what this stack trace means" are false —
// concept questions are answered inline perfectly well, and a false positive
// here costs a subagent round-trip on the most expensive routed model. So, like
// <search>, the detector is precision-biased: a miss merely leaves the main
// model to write the code itself, which is the status quo.
var (
	// Production verb reaching a code artifact. The Chinese branch bounds the
	// gap between verb and object and forbids 的/关 inside it, so "写一个关于爬虫
	// 的故事" (a STORY about crawlers) does not ride the 爬虫 noun.
	coderProduceRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		// zh: produce verb + optional quantifier + short gap + code object. The
		// gap forbids sentence punctuation and 关 (of 关于 "about"), so "写一个
		// 关于爬虫的故事" — a STORY about crawlers — cannot ride the 爬虫 noun.
		`(?:帮我|给我|请|帮忙)?(?:写|实现|开发|搭建?|生成|编写|做|撸|搞|弄|整)\s*(?:一个|一份|一段|一套|个|份|段|套|点)?[^，。！？；、关\n]{0,12}(?:代码|脚本|函数|程序|网页|页面|网站|爬虫|插件|扩展|组件|接口|服务|工具|游戏|应用|小程序|正则|查询语句|单元测试|测试用例|demo|html|css|sql|app|api|bot)`,
		// zh: transform/repair verbs whose object is inherently code
		`重构|改成异步|改写成|优化(?:一下)?(?:这段|这个)?代码|调试(?:一下)?(?:代码|程序|脚本)|修(?:一下|复)(?:这个|一下)?(?:bug|代码|报错|脚本)`,
		// en: produce verb + short gap + code object
		`\b(?:write|implement|build|create|make|code|develop|whip\s+up)\b[^.?!;\n]{0,40}\b(?:script|function|program|web\s?page|website|site|landing\s+page|app|api|endpoint|component|class|module|crawler|scraper|bot|extension|plugin|regex|query|unit\s+tests?|tests?|game|tool|cli|parser|dashboard|html|css|queue|stack|linked\s+list|algorithm|data\s+structure|rate\s+limiter)\b`,
		// en: repair/transform verbs that only ever take code
		`\brefactor\b|\bdebug\b|\bfix\s+(?:this|the|my)\s+(?:bug|code|error|crash|test|script|function)\b`,
	}, "|"))

	// Repair intent next to pasted code: the paste itself (codePasteRE) plus a
	// fix-shaped ask means the user wants the code CHANGED, not explained.
	coderFixIntentRE = regexp.MustCompile(`(?i)修一下|修复|帮我修|改一下|跑不起来|报错了|\bfix\b|\bnot\s+work(?:ing)?\b|\bbroken\b`)
)

// classifyCoderEmbedFn is indirected so tests can pin the regex-only path.
var classifyCoderEmbedFn = classifyCoderEmbed

// needsCoder reports whether the message asks for code to be produced or
// repaired. The embedding classifier has the first word when available — verb
// tables lose on held-out phrasings (the <destructive> lesson) — and the regex
// below is the fallback. ctx bounds the embedding round-trip only.
func needsCoder(ctx context.Context, msg string) bool {
	return needsCoderWith(ctx, msg, classifyCoderEmbedFn)
}

// needsCoderRegex is the same detector with the embedding layer taken away,
// standing as the fallback verdict when the budget blows. See preThinkAction.
func needsCoderRegex(msg string) bool { return needsCoderWith(context.Background(), msg, noEmbed) }

func needsCoderWith(ctx context.Context, msg string, classify embedClassifier) bool {
	// Pasted code with a repair ask is the one case decided before the
	// embedding layer: long pastes truncate poorly, and the deterministic
	// answer is already right.
	if codePasteRE.MatchString(msg) {
		return coderFixIntentRE.MatchString(msg)
	}
	// Union, like <destructive>: the regex is precision-built (verb + code
	// object), so a regex hit stands even when the embedding vote falls short
	// of its margin. The union's false-positive rate is the sum of two layers
	// that each measured zero on their held-out negatives.
	verdict := coderProduceRE.MatchString(msg)
	if embedVerdict, ok := classify(ctx, msg); ok {
		verdict = verdict || embedVerdict
	}
	return verdict
}
