package thread

import "testing"

// Hand-written test set for the <has_web_url> pre-think field. Positives cover
// the shapes users actually paste; negatives are the traps a naive "looks like a
// domain" matcher falls into.
func TestHasWebURL(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		// --- positives ---
		{
			name: "https url with path",
			msg:  "帮我看看 https://github.com/linanwx/nagobot 的 README 写了啥",
			want: true,
		},
		{
			name: "http url with query string",
			msg:  "summarize http://example.com/blog/post?id=42&lang=en for me",
			want: true,
		},
		{
			name: "bare www host, no scheme",
			msg:  "去 www.zhihu.com 搜一下这个问题",
			want: true,
		},
		{
			name: "markdown link followed by full-width punctuation",
			msg:  "参考这个 [Go 文档](https://go.dev/doc/effective_go)。写个例子",
			want: true,
		},
		{
			name: "uppercase scheme and host",
			msg:  "Check HTTPS://Example.COM/status and tell me if it is up",
			want: true,
		},

		// --- negatives ---
		{
			name: "filenames whose extensions are real TLDs",
			msg:  "改一下 main.go 和 install.sh，然后跑 go test ./...",
			want: false,
		},
		{
			name: "email address",
			msg:  "把周报发到 linanwx@gmail.com",
			want: false,
		},
		{
			name: "protocol named as a topic, not a link",
			msg:  "解释一下 HTTP 和 HTTPS 的区别，顺便说说 HTTP/2",
			want: false,
		},
		{
			name: "package name containing http, plus a version",
			msg:  "升级到 v1.5.63 之后 http-proxy 这个依赖报错了",
			want: false,
		},
		{
			// Deliberate divergence from what an LLM would likely answer: a bare
			// host with no scheme and no www. is not an http/https link, and
			// treating it as one costs far more false positives than it gains.
			name: "bare domain in prose, no scheme",
			msg:  "对比一下 openai.com 和 deepseek 的定价",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasWebURL(tc.msg); got != tc.want {
				t.Errorf("hasWebURL(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// Hand-written test set for the <is_include_investigator> pre-think field: the
// user EXPLICITLY asks to search or investigate. Positives span 18 languages;
// negatives are the traps — questions that need a search but never ask for one,
// searches scoped to local artifacts, and verbs that merely contain a search
// morpheme (检查 / "look at" / "research paper").
func TestIsIncludeInvestigator(t *testing.T) {
	cases := []struct {
		lang string
		msg  string
		want bool
	}{
		// --- positives: explicit search / investigate request ---
		{"zh", "搜一下 nagobot 最新版本是多少", true},
		{"zh", "帮我查一下 DeepSeek V4 的定价", true},
		{"zh", "调查一下这家公司的背景和股东结构", true},
		{"zh", "帮我 google 一下 nagobot 的 GitHub star 数", true},
		{"zh", "上网查一下今天美元对人民币的汇率", true},
		{"zh", "百度一下明天北京的天气", true},
		{"zh-Hant", "幫我搜尋一下台北明天的天氣", true},
		{"en", "search for the current pricing of Claude Opus 4.8", true},
		{"en", "google who won the 2026 World Cup", true},
		{"en", "can you look up the population of Shenzhen?", true},
		{"en", "do some research on Go regexp performance", true},
		{"en", "find out when the next SpaceX launch is", true},
		{"en", "investigate why openrouter returns 403 from Chinese IPs", true},
		{"ru", "найди информацию о ценах на GPT-5.6", true},
		{"ru", "загугли, кто сейчас президент Франции", true},
		{"ru", "поищи в интернете отзывы о DeepSeek V4", true},
		{"es", "busca el precio actual del oro", true},
		{"es", "investiga quién ganó el Balón de Oro 2025", true},
		{"pt", "pesquise sobre as novas regras de visto para o Brasil", true},
		{"fr", "cherche des infos sur les tarifs de Mistral", true},
		{"fr", "renseigne-toi sur la météo à Paris demain", true},
		{"de", "Such nach den aktuellen Strompreisen in Berlin", true},
		{"de", "Recherchiere die Marktlage für E-Autos in China", true},
		{"it", "Cerca su internet le recensioni del nuovo iPhone", true},
		{"ja", "最新のAIニュースを調べて", true},
		{"ja", "ググって、東京の明日の天気を教えて", true},
		{"ko", "삼성 갤럭시 S26 가격 검색해줘", true},
		{"ar", "ابحث عن أسعار الذهب اليوم", true},
		{"vi", "tìm kiếm thông tin về giá vé máy bay đi Hà Nội", true},
		{"th", "ค้นหาข้อมูลเกี่ยวกับวีซ่าประเทศไทย", true},
		{"hi", "इंटरनेट पर खोजो कि भारत की जनसंख्या कितनी है", true},
		{"tr", "Türkiye'deki elektrik fiyatlarını araştır", true},
		{"pl", "wyszukaj informacje o cenach gazu w Polsce", true},
		{"id", "cari informasi tentang harga tiket ke Bali", true},
		{"nl", "zoek uit wie de premier van Nederland is", true},

		// --- negatives: needs a search, but never asked for one (<search>'s job) ---
		{"zh", "GPT-5.6 多少钱？", false},
		{"en", "what's the price of gold today?", false},

		// --- negatives: 检查/查看/查询 merely contain 查 ---
		{"zh", "检查一下我的代码有没有 bug", false},
		{"zh", "帮我查看一下 config.yaml 里的 thread.models", false},
		{"zh", "查询数据库里的用户表有多少行", false},

		// --- negatives: search scoped to a local artifact, not the web ---
		{"zh", "在代码库里搜索所有 TODO", false},
		{"en", "search and replace foo with bar in main.go", false},
		{"en", "grep -r 'sessionKey' .", false},
		{"en", "search my codebase for unused exports", false},

		// --- negatives: verbs that only look like a search request ---
		{"en", "look at this stack trace and tell me what's wrong", false},
		{"en", "write a research paper about climate change", false},
		{"zh", "把这段话翻译成英文", false},
		{"ru", "проверь мой код на ошибки", false},
		{"ja", "このコードを見て、リファクタリングして", false},
		{"ko", "이 코드를 리팩터링 해줘", false},
		{"es", "revisa mi código", false},
		{"de", "Schau dir meinen Code an", false},
		{"fr", "vérifie mon code", false},
		{"it", "Controlla il mio codice", false},
	}

	var fails int
	for _, tc := range cases {
		if got := isIncludeInvestigator(tc.msg); got != tc.want {
			fails++
			t.Errorf("[%s] isIncludeInvestigator(%q) = %v, want %v", tc.lang, tc.msg, got, tc.want)
		}
	}
	t.Logf("%d/%d passed", len(cases)-fails, len(cases))
}

// Hand-written test set for the <search> pre-think field: could the fact this
// answer rests on have CHANGED since the cutoff, or does it need an
// authoritative source?
//
// The cases are built as discriminating PAIRS rather than as topic coverage,
// because every naive reading of this field dies on one of them:
//
//	who was Isaac Newton            false   |  who is France's president now  true
//	convert 100 km to miles         false   |  convert 100 USD to CNY         true
//	summarize the text above        false   |  summarize this week's AI news  true
//	which is better, recursion/iter false   |  which is better in 2026, PG/MySQL true
//
// needsSearchCases is shared by TestNeedsSearch (regex fallback path, pinned by
// disabling the embed classifier) and TestNeedsSearch_Embed (classifier path,
// skipped when no local Ollama is present).
var needsSearchCases = []struct {
	lang string
	msg  string
	want bool
}{
	// --- positives: real-time / volatile state ---
	{"zh", "现在美元对人民币的汇率是多少", true},
	{"zh", "明天上海会下雨吗", true},
	{"zh", "现在法国总统是谁", true},
	{"en", "what's the current price of Bitcoin?", true},
	{"ru", "какой сейчас курс доллара к рублю?", true},
	{"fr", "quelle est la météo à Paris demain ?", true},
	{"pt", "qual é a cotação do dólar hoje?", true},
	{"ar", "ما هو سعر الذهب اليوم؟", true},
	{"vi", "giá vàng hôm nay bao nhiêu?", true},

	// --- positives: prices / availability of real products ---
	{"zh", "Claude Opus 4.8 的 API 价格是多少", true},
	{"en", "is the new iPhone available in Japan yet?", true},
	{"de", "Wie hoch sind aktuell die Strompreise in Deutschland?", true},
	{"ja", "最新のiPhoneの価格は？", true},
	{"ko", "삼성 갤럭시 S26 출시일은 언제야? 최신 정보로 알려줘", true},
	{"es", "¿cuál es el precio actual del oro?", true},

	// --- positives: versions / releases / docs ---
	{"zh", "Go 1.26 有哪些新特性", true},
	{"en", "what's the latest version of Kubernetes?", true},
	{"en", "did Anthropic release a new model recently?", true},
	{"zh", "OpenRouter 现在支持哪些模型", true},
	{"en", "how do I configure OpenTelemetry in Go? link me the docs", true},

	// --- positives: reviews / opinion on real products ---
	{"en", "what are the reviews for the Sony WH-1000XM6?", true},
	{"zh", "这款显卡的评测怎么样，值得买吗", true},

	// --- positives: the discriminating halves of the pairs ---
	{"en", "summarize the top AI news from this week", true},    // summarize, but NOT of provided text
	{"en", "convert 100 USD to CNY", true},                      // conversion, but the rate is volatile
	{"en", "which is better in 2026, Postgres or MySQL?", true}, // comparison anchored to a year
	{"ru", "кто выиграл Лигу чемпионов в 2026?", true},

	// --- negatives: stable facts, even with named entities ---
	{"en", "who was Isaac Newton?", false},
	{"en", "what is the capital of France?", false},
	{"en", "explain how the TCP handshake works", false},
	{"fr", "explique-moi la différence entre HTTP et HTTPS", false},
	{"zh", "解释一下什么是闭包", false},

	// --- negatives: pure reasoning / math / code ---
	{"zh", "1+1 为什么等于 2", false},
	{"en", "solve x^2 - 5x + 6 = 0", false},
	{"en", "convert 100 km to miles", false},
	{"en", "which is better, recursion or iteration?", false},
	{"zh", "写一个快速排序", false},
	{"en", "write a binary search function in Go", false},
	{"ja", "このコードをリファクタリングして", false},
	{"en", "debug this stack trace and tell me what broke", false},

	// --- negatives: text ops on user-supplied text (spec-explicit) ---
	{"zh", "把这段话翻译成英文：我今天很累", false},
	{"zh", "帮我润色一下上面这段文字", false},
	{"zh", "总结一下上面这段文字的要点", false},
	{"en", "translate this to Chinese: hello world", false},
	{"en", "rewrite the following paragraph to be more concise", false},
	{"de", "Fasse den folgenden Text zusammen", false},
	{"ru", "переведи это сообщение на английский", false},

	// --- negatives: casual chat (where a bare 今天/today carries no fact) ---
	{"zh", "你好，今天过得怎么样", false},
	{"en", "thanks, that helped!", false},
	{"zh", "哈喽，在吗", false},

	// --- negatives: creative writing with no factual anchor ---
	{"zh", "帮我写一封辞职信", false},
	{"es", "escribe un poema sobre el mar", false},
	{"zh", "写一首关于秋天的诗", false},
}

// TestNeedsSearch exercises the regex fallback path: the embed classifier is
// pinned off so the result is deterministic on machines with and without a
// local Ollama.
func TestNeedsSearch(t *testing.T) {
	old := classifySearchEmbedFn
	classifySearchEmbedFn = func(string) (bool, bool) { return false, false }
	t.Cleanup(func() { classifySearchEmbedFn = old })

	var fails int
	for _, tc := range needsSearchCases {
		if got := needsSearch(tc.msg); got != tc.want {
			fails++
			t.Errorf("[%s] needsSearch(%q) = %v, want %v", tc.lang, tc.msg, got, tc.want)
		}
	}
	t.Logf("%d/%d passed", len(needsSearchCases)-fails, len(needsSearchCases))
}

// TestNeedsSearch_Embed runs the same cases through the embedding classifier.
// Requires a local Ollama with an embedding model; skips otherwise.
func TestNeedsSearch_Embed(t *testing.T) {
	if _, _, ok := searchEmbed.score("probe"); !ok {
		t.Skip("no local ollama embedding model")
	}

	var fails int
	for _, tc := range needsSearchCases {
		got := needsSearch(tc.msg)
		if got != tc.want {
			fails++
			pos, neg, _ := searchEmbed.score(tc.msg)
			t.Errorf("[%s] needsSearch(%q) = %v, want %v (pos=%.4f neg=%.4f margin=%+.4f)",
				tc.lang, tc.msg, got, tc.want, pos, neg, pos-neg)
		}
	}
	t.Logf("%d/%d passed", len(needsSearchCases)-fails, len(needsSearchCases))
}
