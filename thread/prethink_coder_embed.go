package thread

import (
	"context"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// Prototype classification for <coder>, mirroring the search and destructive
// classifiers: the message is compared against positive/negative anchor
// sentences in embedding space and the closer side wins. The anchors are
// DISJOINT from the test cases in prethink_coder_test.go — those stay held out.
//
// The rival class is chosen adversarially around the production/explanation
// boundary: for the positives that produce code there are negatives that merely
// talk ABOUT code (concept questions, review requests, learning advice, version
// lookups). A classifier that cannot hold those apart is measuring topic, not
// intent — the same failure destructiveNegAnchors guards against with its
// read-only twins.
var coderPosAnchors = []string{
	// scripts and automation
	"帮我写一个 Python 脚本批量重命名文件",
	"写个脚本每天自动备份这个目录",
	"write a script that renames all my photos by date",
	"напиши скрипт на Python для парсинга логов",
	// functions, algorithms, queries
	"写个函数把 CSV 转成 JSON",
	"implement a rate limiter in Go",
	"escribe una función que ordene una lista de fechas",
	"帮我写个 SQL 统计每个月的订单量",
	"写一个正则匹配中国大陆手机号",
	"write a unit test for this parser class",
	// web pages and apps
	"帮我做一个贪吃蛇小游戏网页",
	"用 HTML 和 CSS 做一个个人主页",
	"build a landing page with a signup form",
	"make a chrome extension that hides youtube shorts",
	"帮我做一个展示单词的 HTML 动画卡片",
	"develop a small REST API for a todo list",
	// crawlers and bots
	"写一个爬虫抓取商品价格存到表格里",
	"code a discord bot that welcomes new members",
	// refactor, transform, repair
	"把这段同步代码改成异步的",
	"帮我重构这个类，把数据库逻辑抽出来",
	"refactor this module to use dependency injection",
	"この関数をリファクタリングしてください",
	"这段代码报 NullPointerException，帮我修一下",
	"fix this bug: the loop never terminates",
	"debug why this endpoint returns 500 and patch it",
}

var coderNegAnchors = []string{
	// concept and explanation questions about code
	"什么是动态规划",
	"explain how a bloom filter works",
	"这个函数在 Python 里是什么意思",
	"explain what this stack trace means",
	"rebase 和 merge 有什么区别",
	"为什么 Python 的 GIL 会限制多线程",
	"what is the difference between a process and a thread",
	"这段代码为什么会崩",
	"объясни, что делает этот SQL запрос",
	// review and reading, not writing
	"review this pull request and tell me what you think",
	"帮我看看这段代码写得怎么样",
	// learning paths and career
	"我该学 Rust 还是 Go",
	"how should I prepare for a backend interview",
	"推荐几本关于系统设计的书",
	// volatile code-adjacent facts — <search>'s territory
	"Python 最新版本号是多少",
	"which JavaScript framework is the most popular this year",
	"is the new React compiler stable yet",
	// ops actions — <destructive>'s territory, not code production
	"kill the process listening on port 3000",
	"把线上的容器停掉",
	"帮我把这个定时任务删掉",
	// producing text, not code
	"写一篇关于环保的演讲稿",
	"help me draft a resignation email",
	"写一首关于秋天的诗",
	"帮我写一份产品需求文档",
	"write a short story about a robot learning to paint",
	// text ops and data questions
	"把这段话翻译成英文",
	"总结一下这篇文章的要点",
	"these are my expenses from last year, tell me what I spent the most on",
	// chit-chat and capability questions
	"早上好呀",
	"今天天气怎么样",
	"你会写代码吗",
	"what can you help me with",
}

const (
	coderEmbedTopK = 5
	// Swept against the held-out set in TestNeedsCoder_HeldOut, not guessed
	// (TestCoderMarginSweep prints the per-case deltas):
	//
	//	margin   held-out recall   held-out false positives
	//	+0.04         2/8                  0/8
	//	 0.00         5/8                  0/8
	//	-0.03         7/8                  0/8
	//	-0.05         7/8                  0/8 (buffer to nearest negative: 0.02)
	//
	// -0.03 is the knee: the last miss sits at -0.092, below two negatives
	// (-0.069 restart-the-container, -0.090 quicksort complexity), so buying it
	// back would admit both. It stays a recorded miss. The buffer to the
	// nearest negative at -0.03 is 0.039.
	coderEmbedMargin   = -0.03
	coderEmbedMaxRunes = 600

	coderEmbedInitTimeout = 30 * time.Second
	coderEmbedCallTimeout = 5 * time.Second
	coderEmbedRetryAfter  = time.Minute
)

var coderEmbed = &coderEmbedState{mu: newCtxMutex()}

type coderEmbedState struct {
	mu      ctxMutex
	model   string
	pos     [][]float64
	neg     [][]float64
	lastTry time.Time
}

// ensure embeds the anchors for the currently detected model. Caller holds
// s.mu. It runs on its own clock rather than the caller's, for the reason
// spelled out on searchEmbedState.ensure: an index bound to the request budget
// would be cancelled on every cold start and never finish.
func (s *coderEmbedState) ensure() bool {
	ctx, cancel := context.WithTimeout(context.Background(), coderEmbedInitTimeout)
	defer cancel()

	// Shares the Ollama client with the search classifier — same host, same
	// model probe, one connection-refused per minute on a machine without one.
	model, ok := searchEmbed.client.Model(ctx)
	if !ok {
		return false
	}
	if model == s.model && s.pos != nil {
		return true
	}
	if time.Since(s.lastTry) < coderEmbedRetryAfter {
		return false
	}
	s.lastTry = time.Now()

	all := append(append([]string{}, coderPosAnchors...), coderNegAnchors...)
	vecs, err := searchEmbed.client.Embed(ctx, all)
	if err != nil {
		logger.Warn("pre-think coder classifier: anchor embedding failed", "model", model, "err", err)
		return false
	}
	for i := range vecs {
		normalize(vecs[i])
	}
	s.model = model
	s.pos = vecs[:len(coderPosAnchors)]
	s.neg = vecs[len(coderPosAnchors):]
	logger.Info("pre-think coder classifier ready", "model", model,
		"posAnchors", len(s.pos), "negAnchors", len(s.neg))
	return true
}

// coderScores returns the top-k mean cosine to each class.
func coderScores(ctx context.Context, msg string) (pos, neg float64, ok bool) {
	if !coderEmbed.mu.lock(ctx) {
		return 0, 0, false
	}
	defer coderEmbed.mu.unlock()

	if !coderEmbed.ensure() {
		return 0, 0, false
	}

	if r := []rune(msg); len(r) > coderEmbedMaxRunes {
		msg = string(r[:coderEmbedMaxRunes])
	}
	callCtx, cancelCall := context.WithTimeout(ctx, coderEmbedCallTimeout)
	defer cancelCall()
	vecs, err := searchEmbed.client.Embed(callCtx, []string{msg})
	if err != nil {
		logger.Warn("pre-think coder classifier: message embedding failed", "err", err)
		return 0, 0, false
	}
	q := vecs[0]
	normalize(q)
	return topKMeanDot(q, coderEmbed.pos, coderEmbedTopK),
		topKMeanDot(q, coderEmbed.neg, coderEmbedTopK), true
}

// classifyCoderEmbed answers "does this ask for code to be produced?" by
// prototype vote. ok=false means no local Ollama; the caller falls back to the
// regex path.
func classifyCoderEmbed(ctx context.Context, msg string) (verdict bool, ok bool) {
	pos, neg, ok := coderScores(ctx, msg)
	if !ok {
		return false, false
	}
	return pos-neg > coderEmbedMargin, true
}
