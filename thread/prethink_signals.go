package thread

import (
	"context"
	"regexp"
	"strings"
)

// Local signal detectors: deterministic replacements for pre-think fields that
// do not need an LLM. Each mirrors the semantics of the corresponding field in
// cmd/templates/agents/pre-think.md so the action hint stays identical.

// webURLRE matches an explicit http/https URL, or a www.-prefixed host.
//
// A scheme (or the www. prefix) is required. Bare domains are deliberately NOT
// matched: many common TLDs collide with file extensions and package ids that
// users type constantly — main.go, install.sh, deploy.pm, foo.zip are all valid
// TLDs — so matching bare hosts would fire on half the messages in a coding
// session. The pre-think field is defined as "contains an http/https link", and
// requiring the scheme keeps that promise with no false positives.
//
// The host must start with an alphanumeric so a dangling "https://" matches
// nothing. The tail stops at whitespace, bracketing characters, and CJK
// punctuation, so a URL followed by "。" or wrapped in "（）" still matches.
var webURLRE = regexp.MustCompile(`(?i)(\bhttps?://[a-z0-9][^\s<>"'` + "`" + `()\[\]{}，。！？；：、]*|\bwww\.[a-z0-9-]+(\.[a-z0-9-]+)+)`)

// hasWebURL reports whether the message contains a web URL. Replaces the
// pre-think <has_web_url> field.
func hasWebURL(msg string) bool {
	return webURLRE.MatchString(msg)
}

// The <is_include_investigator> field fires only when the user EXPLICITLY asks
// to search or investigate — it forces a dispatch to an investigator subagent.
// It is not "this question would benefit from a search" (that is <search>), so
// "GPT-5.6 多少钱?" is false here while "查一下 GPT-5.6 多少钱" is true.
//
// Detection runs in two passes because the naive one is wrong in every language
// that compounds its verbs:
//
//  1. mask — delete spans where a search-shaped token is NOT an external search
//     request: Chinese compounds that merely contain 查 (检查 / 查看 / 查询), and
//     search verbs whose object is a local artifact (在代码里搜索 / grep / search
//     and replace / search my codebase). Without this, "检查一下代码" matches the
//     "查一下" trigger and every code review turns into a web search.
//  2. match — look for an explicit search/investigate imperative in what is left.
//
// Two hard lessons from sweeping a real multilingual corpus, both encoded below:
//
//   - A bare verb is not a request. "search" appears in every pasted snippet of
//     code, "поиск" is a noun, "Найдите длину" is a geometry problem, and
//     "keyword research expert" is roleplay. Triggers therefore demand a verb
//     PLUS its object or preposition — search FOR, найди ИНФОРМАЦИЮ, 谷歌一下.
//   - Non-ASCII has no \b to lean on, so substring hits are silent and lethal:
//     Italian "informati" lives inside English "information", "investiga" inside
//     "investigation", and Arabic ابحث's cousin بحث inside البحث ("the search",
//     as in "search engines"). Every Latin trigger is \b-anchored; the non-Latin
//     ones are spelled out in full.
var (
	investigatorMaskRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		// Chinese compounds containing 查 that never mean "search the web".
		`检查|查看|查询|审查|排查|复查|抽查`,
		// search verb → local artifact ("搜一下代码", "search my codebase").
		`(?:搜索|搜寻|搜尋|搜|查找|查阅|查|检索|grep|search|find|look\s+up)\s*(?:一下)?\s*(?:这个|那个|my|the|this)?\s*(?:代码库|代码|源码|文件|日志|数据库|仓库|会话|历史|记录|codebase|repo|database|file|log|session|history)`,
		// local artifact → search verb ("在代码库里搜索 TODO").
		`(?:在|从|in|inside|from)?\s*(?:代码库|代码|源码|文件|日志|数据库|仓库|会话|历史|codebase|repo|database|file|log)\s*(?:里面|里|中|内)?\s*(?:的)?\s*(?:搜索|搜寻|搜|查找|检索|grep|search)`,
		// A search noun, not a search request: "搜索引擎", "search engine", "the
		// search bar", "محركات البحث".
		`搜索(?:引擎|功能|框|栏|结果|接口|算法)|\bsearch\s+(?:engine|engines|bar|box|field|function|results?|api|query)\b|محركات\s+البحث`,
		// English phrases that borrow a search verb for something else.
		`\bgrep\b|\bsearch\s+and\s+replace\b|\bresearch\s+(?:paper|papers|article|report|proposal|question|expert)\b|\bkeyword\s+research\b`,
	}, "|"))

	investigatorAskRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		// zh (simplified + traditional)
		`搜一下|搜搜|搜索一下|搜索下|搜寻|搜尋|搜集|帮我搜|幫我搜|查一下|查查|查证|查資料|查资料|帮我查|幫我查|调查|調查|调研|检索一下|檢索一下|谷歌一下|用谷歌|谷歌搜|百度一下|用百度|上网查|網上查|网上查|上网搜|网上搜`,
		// en — verb + object/preposition, never the bare verb
		`\bsearch\s+(?:for|up|the\s+web|online|the\s+internet)\b|\bweb\s+search\b|\bgoogle\s+(?:it|this|that|for|who|what|when|where|why|how|the|a|an)\b|\bgoogle\s*一下|\blook\s+(?:it\s+)?up\b|\blook\s+into\b|\bfind\s+out\b|\b(?:do|conduct|run)\s+(?:some\s+)?research\b|\bresearch\s+(?:on|about|into)\b|\binvestigate\b|\bdig\s+into\b`,
		// ru
		`загугли|погугли|поищи|разузнай|\bисследуй\b|найди\s+(?:информаци|в\s+интернете)|найти\s+информаци|поиск\s+в\s+интернете`,
		// es
		`\bbusca\b|\bbúscame\b|\baverigua\b|\bgooglea\b|\binvestiga\b|\binvestigue\b`,
		// pt
		`\bpesquise\b|\bpesquisar\b|\bprocure\s+(?:por|informa)`,
		// fr
		`\bcherche\b|\brenseigne-toi\b|\bgooglise\b|\bfais\s+des\s+recherches\b`,
		// de
		`\bsuche?\s+nach\b|\brecherchier`,
		// it
		`\bcerca\s+(?:su|informazioni|online|in\s+rete)\b|\bindaga\b|\binformati\s+su\b`,
		// ja
		`調べて|調査して|検索して|ググ`,
		// ko
		`검색해|찾아봐|조사해`,
		// ar — ابحث only; بحث alone lives inside البحث ("the search")
		`ابحث`,
		// vi
		`\btìm\s+kiếm\b|\btra\s+cứu\b|\btìm\s+hiểu\b`,
		// th
		`ค้นหา|หาข้อมูล`,
		// hi
		`खोजो|खोजें|पता\s+करो`,
		// tr
		`\baraştır\b|\barastir\b`,
		// pl
		`\bwyszukaj\b|\bposzukaj\b`,
		// id
		`\bcari\s+informasi\b|\btelusuri\b`,
		// nl
		`\bzoek\s+(?:uit|op|naar)\b`,
	}, "|"))
)

// isIncludeInvestigator reports whether the user explicitly asked to search or
// investigate. Replaces the pre-think <is_include_investigator> field.
func isIncludeInvestigator(msg string) bool {
	return investigatorAskRE.MatchString(investigatorMaskRE.ReplaceAllString(msg, " "))
}

// The <search> field asks a harder question than the two above: not "did the
// user say the word search" but "could the fact this answer rests on have
// CHANGED since the model was trained, or does it need an authoritative source".
// Named entities alone mean nothing — "who was Isaac Newton" is false and "who
// is the president of France now" is true, and the only difference is that one
// role turns over.
//
// So the detector never looks for topics; it looks for VOLATILITY markers:
// temporal deixis (现在 / latest / today / 2026), and domains whose facts have a
// short shelf life (prices, exchange rates, weather, news, versions, releases,
// availability, reviews, docs).
//
// Two spec-mandated negatives are masked first, because they can carry a
// volatility word without needing a source:
//
//   - translation, rewriting, and summarizing of text the USER supplied. Note
//     the qualifier: "总结一下上面这段文字" is false, but "总结一下本周的 AI 新闻"
//     is TRUE — same verb, and only the presence of a provided-text marker
//     tells them apart.
//   - greetings and small talk, which is the one place a bare "今天" / "today"
//     shows up with no fact behind it ("你好，今天过得怎么样").
var (
	// Markers that the object of the task is text the user pasted in.
	providedTextRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		`这段(?:话|文字|文本|内容)?|上面这段|上面的?(?:文字|内容|文章)|以下(?:内容|文字|文本|这段)|下面这段|下面的?(?:文字|内容)`,
		`\b(?:the\s+)?following\s+(?:text|paragraph|passage|article|content)\b|\bthis\s+(?:text|paragraph|passage|article|sentence)\b`,
		`folgenden\s+text|это\s+(?:сообщение|текст)|この(?:文章|テキスト)`,
	}, "|"))

	// Verbs that operate ON text rather than look facts UP.
	textOpRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		`润色|改写|缩写|扩写|总结|概括|摘要`,
		`\brewrite\b|\brephrase\b|\bparaphrase\b|\bpolish\b|\bsummari[sz]e\b|zusammenfass`,
	}, "|"))

	// Translation is a text op no matter what its object is (spec: always false).
	translateRE = regexp.MustCompile(`(?i)翻译|翻譯|\btranslate\b|\btraduis\b|übersetze|번역|переведи|ترجم`)

	// Jailbreak / persona-setup preambles: the message programs the assistant,
	// it doesn't ask about the world.
	promptSetupRE = regexp.MustCompile(`(?i)ignore\s+(?:all\s+)?(?:the\s+)?previous\s+instructions|developer\s+mode|from\s+now\s+on,?\s+you\s+(?:are|will)|you\s+are\s+going\s+to\s+act\s+as`)

	// Small talk: the one context where a bare "today" carries no fact.
	// The CJK greetings carry no \b — RE2's \b is ASCII-only, so "你好\b" can
	// never match (好 is not a word char, and neither is the "，" after it).
	smallTalkRE = regexp.MustCompile(`(?i)^\s*(?:你好|您好|哈喽|嗨|嘿|早上好|晚上好)|^\s*(?:hi|hello|hey)\b|谢谢|感谢|\bthanks\b|\bthank\s+you\b`)

	// Volatility markers. Any hit means the answer may rest on a fact with a
	// short shelf life.
	searchSignalRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		// --- temporal deixis ---
		`现在|目前|当前|如今|眼下|实时|最近|近期|最新|今天|今晚|明天|后天|今年|去年|本周|这周|本月`,
		`\b(?:latest|current|currently|recent|recently|nowadays|today|tonight|tomorrow|right\s+now|up[-\s]to[-\s]date|so\s+far)\b|\bthis\s+(?:week|month|year)\b`,
		`\b20[2-9]\d\b`, // a concrete recent/future year
		`сейчас|сегодня|последн|текущ|актуальн|actualmente|\bactual\b|\bhoy\b|reciente|aujourd'hui|actuel|récent|demain|aktuell|\bheute\b|neueste|\bmorgen\b|最新|現在|今日|明日|지금|최신|오늘|현재|\batual\b|\bhoje\b|اليوم|الآن|hôm\s+nay|hiện\s+tại|mới\s+nhất`,
		// --- prices, markets, money ---
		`价格|多少钱|报价|定价|费用|收费|汇率|股价|行情|市值|涨价|降价`,
		`\bprice|\bpricing\b|\bcost\b|\bhow\s+much\b|\bexchange\s+rate\b|\bstock\b|\bquote\b`,
		`цена|курс\s+доллара|стоимость|precio|cotización|prix|preis|価格|가격|preço|سعر|giá`,
		`\b(?:usd|cny|eur|jpy|gbp|rmb|btc|eth)\b|美元|人民币|欧元|日元`,
		// --- real-time state of the world ---
		`天气|气温|下雨|航班|比分|新闻|头条|热搜`,
		`\bweather\b|\bforecast\b|\bflight\b|\bscore\b|\bnews\b|\bheadlines\b`,
		`погода|новости|clima|tiempo|météo|wetter|天気|ニュース|날씨|뉴스|طقس|thời\s+tiết`,
		// --- versions, releases, availability ---
		`版本|新特性|更新日志|发布了?|上线|发售|上市|有货|库存|支持哪些`,
		`\bversion\b|\brelease[ds]?\b|\bchangelog\b|\bnew\s+features?\b|\bavailable\b|\bavailability\b|\bin\s+stock\b|\bsupported\b`,
		// --- reviews, opinion, comparison of real products ---
		`评测|测评|口碑|评价|值得买|性价比|排行|销量`,
		`\breviews?\b|\bbenchmarks?\b|\branking\b|\bworth\s+buying\b`,
		// --- authoritative documentation ---
		`官方文档|api\s*文档|\bdocs\b|\bdocumentation\b`,
	}, "|"))
)

// A volatility word alone is not enough, and a corpus sweep says so loudly: an
// unweighted keyword match fires on ~15% of real messages and is wrong about
// 85% of the time. Three structural reasons, each answered by a gate below:
//
//   - Long pastes always hit. Any 2000-word essay, code dump, or transcript
//     contains SOME volatility word, so substring matching over a whole message
//     approaches certainty as the message grows. → codePasteRE.
//   - Word senses collide. 评价 is a product review AND an essay's "assessment";
//     review is a product review AND a literature review; price and cost are
//     variable names; version is "a version of the story"; current is electric
//     current. No lexicon fixes this — only context does.
//   - The task verb outranks the topic noun. "写一篇 800 字的表态发言" (contains
//     明天) and "write a unittest for aggregate_trades" (contains price) need no
//     source at all. What decides is what the user asked you to DO, not which
//     nouns drifted by. → productionTaskRE + requestShapeRE.
var (
	// Asking the model to PRODUCE something (write / code / roleplay), not to
	// look a fact up. The dominant false-positive class in the wild.
	productionTaskRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		`写一?[篇封个份首段]|帮我写|请写|代写|创作|编写|生成|起草|续写|扮演|角色扮演`,
		`\bwrite\s+(?:a|an|me|the)\b|\bcompose\b|\bdraft\b|\bgenerate\b|\bcreate\s+(?:a|an)\b|\bact\s+as\b|\bpretend\b|\broleplay\b|\bignore\s+(?:all\s+)?previous\s+instructions\b`,
		`\b(?:essay|story|poem|novel|script|unittest|unit\s+test)\b`,
		`重构|优化(?:一下)?(?:代码|这段)|调试|修复`,
		`\brefactor\b|\bdebug\b|\bimplement\b|\bfix\s+(?:this|the)\b`,
	}, "|"))

	// Pasted source code or a stack trace: the volatility word is in the data,
	// not in the question.
	codePasteRE = regexp.MustCompile("```|(?i)(?:^|\n)\\s*(?:import|from|def|class|const|let|var|func|public|#include|package)\\s|SyntaxError|Traceback|\\bexception\\b")

	// The message is shaped like a request for a fact — a question, or an
	// imperative that asks for information back.
	requestShapeRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
		`[?？]|吗[?？]?$|呢[?？]?$`,
		`什么|多少|哪个|哪些|哪家|谁|何时|几点|怎么样|是不是|有没有|贵不贵`,
		`\b(?:what|which|who|whom|when|where|how|why|is|are|was|were|does|do|did|can|could|should|has|have)\b`,
		`告诉我|给我列|列出|查查看|对比一下`,
		`\btell\s+me\b|\blist\b|\bshow\s+me\b|\bgive\s+me\b|\bcompare\b|\bconvert\b|\bsummari[sz]e\b|\brecommend\b`,
		`какой|сколько|кто|где|когда|cuál|cuánto|quién|quelle|combien|wie\s+hoch|wie\s+viel|welche|얼마|언제|いくら|何|بكم|ما\s+هو|bao\s+nhiêu`,
	}, "|"))
)

// needsSearch reports whether the answer may rest on a fact that has changed
// since the model's cutoff, or needs an authoritative source. Replaces the
// pre-think <search> field.
//
// Spec-explicit negatives are decided by the cheap deterministic masks. For
// everything else the embedding classifier (remote backend, see prethink_embed.go)
// has the first word — it is the only layer that survives word-sense collisions.
// Without a backend the keyword path below is the fallback.
// ctx bounds the embedding round-trip; when it expires the classifier reports
// itself unavailable and the keyword path answers.
func needsSearch(ctx context.Context, msg string) bool {
	return needsSearchWith(ctx, msg, classifySearchEmbedFn)
}

// needsSearchRegex is the same detector with the embedding layer taken away. It
// exists because preThinkAction runs the embedding classifiers under a wall-clock
// budget: when the backend is slow enough to blow it, the turn still needs an answer,
// and the honest answer is the one the regex alone can give rather than a silent
// false. See preThinkAction in prethink.go.
func needsSearchRegex(msg string) bool { return needsSearchWith(context.Background(), msg, noEmbed) }

// embedClassifier is a detector's window onto the embedding layer. Returning
// ok=false means "unavailable", which is what a missing backend, an overrun
// budget, and a cancelled context all amount to.
type embedClassifier func(context.Context, string) (bool, bool)

func noEmbed(context.Context, string) (bool, bool) { return false, false }

func needsSearchWith(ctx context.Context, msg string, classify embedClassifier) bool {
	// Spec-explicit negatives first.
	if translateRE.MatchString(msg) {
		return false
	}
	if textOpRE.MatchString(msg) && providedTextRE.MatchString(msg) {
		return false
	}
	if smallTalkRE.MatchString(msg) {
		return false
	}
	// Pasted code/stack traces: the volatility words are in the data, not the
	// question — and long pastes defeat embedding truncation too.
	if codePasteRE.MatchString(msg) {
		return false
	}
	// Structural gates, each bought by a corpus false-positive class:
	//   - URL present: "summarize the article at <link>" is a FETCH task; the
	//     has_web_url signal already routes it, a search dispatch is redundant.
	//   - under 5 runes ("5+5", "TEST"): too short to embed meaningfully, and
	//     no search-worthy question fits.
	//   - over 1200 runes: kilobyte-scale messages are tasks over embedded
	//     content (essays, agent preambles, memos), never fact lookups.
	//   - persona preamble: jailbreaks and roleplay setups.
	if hasWebURL(msg) || promptSetupRE.MatchString(msg) {
		return false
	}
	if r := []rune(msg); len(r) < 5 || len(r) > 1200 {
		return false
	}
	if verdict, ok := classify(ctx, msg); ok {
		return verdict
	}
	// Regex fallback. Produce-something tasks do not rest on a live fact,
	// however many volatility nouns drift through them.
	if productionTaskRE.MatchString(msg) {
		return false
	}
	// What is left must both ask for a fact and touch a volatile one.
	return requestShapeRE.MatchString(msg) && searchSignalRE.MatchString(msg)
}
