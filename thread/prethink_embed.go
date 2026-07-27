package thread

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/embedding"
	"github.com/linanwx/nagobot/logger"
)

// The regex gate for <search> tops out around 30% precision on real traffic:
// the failures are word-sense collisions (评价 the essay verb vs 评价 the product
// review, "version" of a story vs of a package) and keyword-free positives
// ("is winrm timeout in seconds or milliseconds"). Those need meaning, not
// morphemes — so when a remote embedding backend is configured (see
// prethink_backend.go), the verdict comes from prototype classification: the
// message is compared against hand-written positive/negative anchor sentences
// in embedding space, and the closer side wins. No backend → callers fall back
// to the regex path.
//
// The anchors are deliberately DISJOINT from the test cases in
// prethink_signals_test.go — those stay held out. Several negatives are
// transcribed from real false positives observed in a WildChat corpus sweep
// (available drives, 如何评价 essays, story "versions", price-named variables).
var searchPosAnchors = []string{
	// prices, rates, markets
	"今天黄金价格多少钱一克",
	"美元现在的汇率是多少",
	"特斯拉 Model 3 现在卖多少钱",
	"what is the price of Bitcoin today",
	"how much does ChatGPT Plus cost now",
	"какая сейчас цена биткоина",
	"cuánto cuesta el iPhone ahora",
	"환율 지금 얼마야",
	"convert 500 euros to dollars at the current rate",
	"which cloud provider is the cheapest right now",
	// weather, schedules, live state
	"深圳明天天气怎么样",
	"current weather in Berlin",
	"какая погода в Москве завтра",
	"東京の明日の天気は？",
	"when is the next SpaceX launch",
	// news, roles, recent events
	"这周有什么重要的科技新闻",
	"recent news about the EU AI Act",
	"who is the CEO of OpenAI right now",
	"现在英国首相是谁",
	"who won the champions league final this season",
	// versions, releases, availability, service capabilities
	"Python 最新版本号是多少",
	"latest stable version of Node.js",
	"最新のGoのバージョンは？",
	"iPhone 17 在日本上市了吗",
	"is the RTX 5090 in stock anywhere",
	"what new AI models were released this month",
	"what payment methods does Stripe currently support",
	// reviews, market comparisons, current docs
	"best mirrorless camera this year according to reviews",
	"which JavaScript framework is the most popular this year",
	"which cloud platform is better in 2026, AWS or Azure",
	"这款笔记本的评测和口碑怎么样",
	"哪种农产品今年市场行情最好",
	"what does the official documentation say about the default timeout unit",
	"去日本的签证政策最近有变化吗",
}

var searchNegAnchors = []string{
	// stable knowledge, homework, quizzes
	"who invented the telephone",
	"what is the capital of Japan",
	"explain quantum entanglement simply",
	"quelle est la différence entre TCP et UDP",
	"什么是动态规划",
	"牛顿第二定律是什么",
	"为什么天空是蓝色的",
	"how far is the moon from the earth",
	"which organ produces insulin, the liver or the pancreas",
	"cuéntame datos interesantes sobre los dinosaurios",
	"calcola la forza tra due cariche elettriche",
	"这个函数在 Python 里是什么意思",
	// math, pure reasoning, stable conversions, word problems
	"solve 2x + 3 = 11",
	"把 37 转换成二进制",
	"convert 100 fahrenheit to celsius",
	"which is better, composition or inheritance",
	"a shirt costs 40 dollars and is discounted by 15 percent, what is the final price",
	"how many usable host addresses does a /26 subnet have",
	// code work (incl. price-named variables — a real corpus trap)
	"write a function to reverse a linked list",
	"refactor this class to use dependency injection",
	"why does this null pointer exception happen",
	"帮我把这段代码改成异步的",
	"write a unit test for the calculate_price function",
	"оптимизируй этот SQL запрос",
	// writing and creative production
	"写一篇关于环保的演讲稿",
	"help me draft a resignation email",
	"write a poem about the ocean",
	"escribe una historia corta de terror",
	"帮我写一份产品需求文档",
	"エッセイを書いてください",
	"напиши уникальный SEO текст на тему путешествий",
	"напиши описание товара для интернет-магазина",
	// roleplay
	"let's roleplay, you are a detective in 1920s Paris",
	"假装你是我的面试官，问我三个问题",
	// operations on user-supplied text/data
	"translate this paragraph into German",
	"把下面这段话总结成三点",
	"proofread the following essay",
	"these are my expenses from last year, tell me what I spent the most on",
	// chit-chat and assistant self-identity
	"早上好呀",
	"how are you doing today",
	"谢谢你的帮助",
	"what model version are you running",
	"are you GPT-4 or GPT-3, which model am I talking to",
	"привет, ты какая версия ChatGPT?",
	"isn't this year wonderful",
	// corpus-observed traps
	"design a NAS layout, the available drives are four 4TB disks",
	"如何评价王安石变法的历史意义",
	"in this story, the forest is an older version of the real world",
	"should I learn Rust or Go as a beginner",
	"我和女朋友吵架了，我该怎么办",
}

// searchEmbedTask is the instruction the search classifier embeds its ANCHORS
// under (see qwen3Instructed). Both-sides instruction was compared against
// query-side-only on the 4B: both score 50/52 on the hand set with a different
// pair of borderline misses; both-sides is kept for symmetry with destructive
// and because its worst miss is nearer the threshold (-0.0035 vs -0.0029 on a
// different case, -0.041 vs -0.019 on the shared one).
//
// It aliases preThinkQueryTask rather than repeating the string: the query side
// is now shared with every other classifier, and two constants holding the same
// sentence is how the three of them drifted into issuing identical requests
// without anyone noticing.
const searchEmbedTask = preThinkQueryTask

const (
	searchEmbedTopK = 6
	// Decision threshold on (posScore - negScore), calibrated against the
	// hand-written set in TestNeedsSearch_Embed. Zero means "whichever side is
	// nearer wins"; a small positive margin biases toward false because a
	// wrong true costs a pointless search dispatch.
	searchEmbedMargin = 0.0

	searchEmbedInitTimeout = 30 * time.Second
	searchEmbedCallTimeout = 5 * time.Second
	searchEmbedRetryAfter  = time.Minute
)

// classifySearchEmbedFn is indirected so tests can pin the regex-only path.
var classifySearchEmbedFn = classifySearchEmbed

var searchEmbed = &searchEmbedState{client: embedding.NewChain(resolveEmbeddingBackend), mu: newCtxMutex()}

// qwen3Instructed wraps text in the instruction format Qwen3-Embedding is
// trained with. It is worth real points: on the destructive set, the raw-text
// 4B scores 2 misses / 13∕15 held-out, the instructed 4B 0 misses / 15∕15.
// Which SIDES get the prefix is measured per classifier, not assumed —
// destructive and search instruct both sides, coder instructs the query only
// (see coderEmbedTask for the numbers), and skill retrieval is query-side-only
// by construction (see qwen3Instruct). Non-qwen3 models get the bare text: a
// prefix they were not trained on is just noise.
func qwen3Instructed(model, task, text string) string {
	if !strings.Contains(strings.ToLower(model), "qwen3-embedding") {
		return text
	}
	return "Instruct: " + task + "\nQuery: " + text
}

// ctxMutex is a mutex you are allowed to give up on.
//
// Each classifier serializes on its own index, and building one against a cold
// or wedged backend holds the lock for seconds. With a plain sync.Mutex every
// message arriving in that window parks a goroutine behind it — goroutines
// nobody is waiting for any more, because localPreThink gave up at preThinkBudget
// and already answered from the regex verdicts. They wake up much later, embed a
// message whose turn is long over, and pile up in the meantime. Taking the lock
// through the caller's context lets them leave when the caller does.
type ctxMutex chan struct{}

func newCtxMutex() ctxMutex { return make(ctxMutex, 1) }

// lock reports whether the lock was acquired before ctx was done. A false return
// is not an error: the caller reports itself unavailable and the regex verdict
// stands.
func (m ctxMutex) lock(ctx context.Context) bool {
	select {
	case m <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m ctxMutex) unlock() { <-m }

// searchEmbedState lazily embeds the anchors and caches them per model. The
// cache re-arms when detection reports a different model (e.g. the user pulls
// a better one mid-run), and failed inits retry after a cooldown instead of
// latching off forever.
type searchEmbedState struct {
	client *embedding.Client

	mu      ctxMutex
	model   string
	pos     [][]float64
	neg     [][]float64
	lastTry time.Time
}

// ensure returns true when anchor vectors for the currently detected model are
// ready. It must be called with s.mu held.
//
// It deliberately does NOT take the caller's context. Building the index is a
// one-time cost that a cold or slow backend can stretch to several seconds — past
// preThinkBudget — and binding it to the request would mean it gets cancelled
// every time, then declines to retry for a minute (lastTry), then gets cancelled
// again: the index would never finish and the embedding layer would stay off
// forever. So the build runs to completion on its own clock. The message it was
// building for is already lost — localPreThink answered from regex — but every
// message after it finds the index warm, which is the whole point.
func (s *searchEmbedState) ensure() bool {
	ctx, cancel := context.WithTimeout(context.Background(), searchEmbedInitTimeout)
	defer cancel()

	model, ok := s.client.Model(ctx)
	if !ok {
		return false // no backend configured — feature off, not an error
	}
	if model == s.model && s.pos != nil {
		return true
	}
	if time.Since(s.lastTry) < searchEmbedRetryAfter {
		return false
	}
	s.lastTry = time.Now()

	texts := make([]string, 0, len(searchPosAnchors)+len(searchNegAnchors))
	for _, a := range append(append([]string{}, searchPosAnchors...), searchNegAnchors...) {
		texts = append(texts, qwen3Instructed(model, searchEmbedTask, a))
	}
	vecs, err := s.client.Embed(ctx, texts)
	if err != nil {
		logger.Warn("pre-think search classifier: anchor embedding failed", "model", model, "err", err)
		return false
	}
	for i := range vecs {
		normalize(vecs[i])
	}
	s.model = model
	s.pos = vecs[:len(searchPosAnchors)]
	s.neg = vecs[len(searchPosAnchors):]
	logger.Info("pre-think search classifier ready", "model", model,
		"posAnchors", len(s.pos), "negAnchors", len(s.neg))
	return true
}

// score returns mean top-k cosine similarity to each anchor side. ok=false
// means the classifier is unavailable and the caller should fall back to the
// regex path.
//
// The query vector comes from the shared per-message embedder when one is on
// the context (the production path — one round trip for all four classifiers);
// otherwise it is embedded here on demand, which is what direct test callers
// and WarmLocalPreThink get.
//
// Unlike the index build, the per-message embedding IS bound by ctx: it is work
// done for one turn and worthless once that turn has moved on, so when the budget
// blows the HTTP request is cancelled rather than left running against a backend
// that is already struggling.
func (s *searchEmbedState) score(ctx context.Context, msg string) (pos, neg float64, ok bool) {
	if !s.mu.lock(ctx) {
		return 0, 0, false
	}
	defer s.mu.unlock()

	if !s.ensure() {
		return 0, 0, false
	}

	callCtx, cancelCall := context.WithTimeout(ctx, searchEmbedCallTimeout)
	defer cancelCall()
	q, err := queryVector(callCtx, s.client.Embed, preThinkQuery(s.model, msg))
	if err != nil {
		logger.Warn("pre-think search classifier: message embedding failed", "err", err)
		return 0, 0, false
	}
	return topKMeanDot(q, s.pos, searchEmbedTopK), topKMeanDot(q, s.neg, searchEmbedTopK), true
}

// classifySearchEmbed answers the <search> question by prototype vote.
func classifySearchEmbed(ctx context.Context, msg string) (verdict bool, ok bool) {
	pos, neg, ok := searchEmbed.score(ctx, msg)
	if !ok {
		return false, false
	}
	return pos-neg > searchEmbedMargin, true
}

func normalize(v []float64) {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return
	}
	inv := 1 / math.Sqrt(sum)
	for i := range v {
		v[i] *= inv
	}
}

// topKMeanDot returns the mean of the k highest dot products between q and the
// (normalized) anchors.
func topKMeanDot(q []float64, anchors [][]float64, k int) float64 {
	sims := make([]float64, 0, len(anchors))
	for _, a := range anchors {
		var dot float64
		for i := range q {
			dot += q[i] * a[i]
		}
		sims = append(sims, dot)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(sims)))
	if k > len(sims) {
		k = len(sims)
	}
	var sum float64
	for _, s := range sims[:k] {
		sum += s
	}
	return sum / float64(k)
}
