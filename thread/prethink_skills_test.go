package thread

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// loadRealSkills reads the canonical skill templates so the test pool is the
// SAME one the pre-think prompt renders as {{SKILLS}} — retrieval quality is
// entirely a function of these descriptions, so testing against invented ones
// would prove nothing.
func loadRealSkills(t *testing.T) []skillCandidate {
	t.Helper()

	root := filepath.Join("..", "cmd", "templates", "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read skills dir: %v", err)
	}

	var cands []skillCandidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		// Front matter is delimited by --- ... ---
		parts := strings.SplitN(string(raw), "---", 3)
		if len(parts) < 3 {
			continue
		}
		var fm struct {
			Description string   `yaml:"description"`
			Examples    []string `yaml:"examples"`
		}
		if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
			continue
		}
		cands = append(cands, skillCandidate{
			Slug:        e.Name(),
			Description: fm.Description,
			Examples:    fm.Examples,
		})
	}
	if len(cands) < 20 {
		t.Fatalf("expected the real skill pool, got %d", len(cands))
	}
	return cands
}

// skillCases: multilingual messages mapped to the skill that must be retrieved.
// want == "" means NO skill should be offered — the pre-think prompt explicitly
// says to omit rather than pad, and an eager LLM is bad at exactly that.
//
// `known` documents a measured limitation: the case does not pass today, the
// reason is understood, and it is recorded rather than tuned away. Every one of
// them traces back to a skill DESCRIPTION that reads well to an LLM but retrieves
// badly — which is worth knowing, because the same descriptions are what the
// LLM path routes on too. Fixing them improves both paths.
var skillCases = []struct {
	lang  string
	msg   string
	want  string // required slug, or "" for "no skill fits"
	known string // non-empty: documented miss, logged instead of failed
}{
	// --- create-html ---
	{"zh", "帮我做一个 PPT，讲一下这个季度的业绩", "create-html", ""},
	{"en", "turn these sales numbers into a chart I can share", "create-html", ""},
	{"de", "Erstelle einen Report als Webseite mit Diagrammen", "create-html", ""},

	// --- image ---
	{"zh", "画一张赛博朋克风格的猫的插画", "image", ""},
	{"en", "generate a poster with the text 'Grand Opening' on it", "image", ""},
	{"ja", "猫の写真をアニメ風に加工して", "image", ""},

	// --- send-image / send-docs ---
	{"zh", "把刚才生成的那张图发给我", "send-image", ""},
	{"en", "send me that PDF file as an attachment", "send-docs", ""},

	// --- manage-cron ---
	{"zh", "每天早上八点提醒我喝水", "manage-cron", ""},
	{"en", "schedule a weekly report every Monday at 9am", "manage-cron", ""},
	{"de", "Erinnere mich jeden Montag an das Team-Meeting", "manage-cron", ""},
	{"ru", "напоминай мне каждый день в 10 утра про таблетки", "manage-cron", ""},

	// --- manage-config ---
	{"zh", "帮我把 DeepSeek 的 API key 换成新的", "manage-config", ""},
	{"en", "switch the default model to gpt-5.6", "manage-config", ""},
	{"fr", "configure la clé API pour OpenRouter", "manage-config", ""},

	// --- manage-channels ---
	{"zh", "配置一下 Telegram 的 bot token", "manage-channels", ""},
	{"en", "my Discord bot is not responding, check the channel setup", "manage-channels", ""},

	// --- manage-skills / manage-agents ---
	{"zh", "有没有能处理 Excel 的技能？去装一个", "manage-skills", ""},
	{"en", "create a new agent that writes weekly reports", "manage-agents", ""},

	// --- monitoring ---
	{"zh", "看看我的 API 余额还剩多少", "monitoring", ""},
	{"en", "how many tokens did I burn this week?", "monitoring", ""},
	{"ru", "покажи статистику по расходам на токены", "monitoring", ""},

	// --- session-ops / context-ops ---
	{"zh", "我现在这个会话用的是哪个模型？", "session-ops", ""},
	{"en", "the context is too long, compress it", "context-ops", ""},

	// --- apple-apps ---
	{"zh", "在日历里加一个明天下午三点的会议", "apple-apps", ""},
	{"en", "turn on dark mode on my mac", "apple-apps", ""},

	// --- thread-ops / file-track ---
	{"en", "spawn a subagent to handle this in the background", "thread-ops", ""},
	{"zh", "整理一下这个会话目录里的文件", "file-track", ""},

	// --- third-party-skill-setup ---
	{"zh", "帮我把 playwright 装上", "third-party-skill-setup", ""},

	// --- web-search-guide / web-fetch-guide ---
	{"en", "web_search returns garbage, switch to another source", "web-search-guide", ""},
	{"zh", "web_fetch 报 403 了，换个抓取源", "web-fetch-guide", ""},

	// --- negatives: no skill fits ---
	{"zh", "你好，在吗", "", ""},
	{"en", "thanks, that was helpful", "", ""},
	{"zh", "1+1 为什么等于 2", "", ""},
	{"en", "explain how the TCP handshake works", "", ""},
	{"zh", "把这段话翻译成英文：我今天很累", "", ""},
	{"en", "write a binary search function in Go", "", ""},
	{"zh", "写一首关于秋天的诗", "", ""},
	{"ru", "расскажи анекдот", "", ""},
}

func TestRelatedSkills(t *testing.T) {
	cands := loadRealSkills(t)
	if _, ok := relatedSkillsEmbed("probe", cands); !ok {
		t.Skip("no local ollama embedding model")
	}

	var hit, cleanNeg, totalSlugs int
	for _, tc := range skillCases {
		got, ok := relatedSkillsEmbed(tc.msg, cands)
		if !ok {
			t.Fatal("classifier went unavailable mid-run")
		}
		totalSlugs += len(got)

		// Hard invariant, never waived: a user message must not be able to
		// trigger a scheduler-only skill (dream, *-dispatcher, *-updater).
		for _, slug := range got {
			for _, c := range cands {
				if c.Slug == slug && neverCallDirectly(c.Description) {
					t.Errorf("[%s] scheduler-only skill %q offered for %q", tc.lang, slug, tc.msg)
				}
			}
		}

		pass := (tc.want == "" && len(got) == 0) || (tc.want != "" && contains(got, tc.want))
		switch {
		case pass && tc.known != "":
			t.Errorf("[%s] %q now passes — drop its `known` note (%s)", tc.lang, tc.msg, tc.known)
		case pass && tc.want == "":
			cleanNeg++
		case pass:
			hit++
		case tc.known != "":
			t.Logf("known miss [%s] %q → %v (want %q): %s", tc.lang, tc.msg, got, tc.want, tc.known)
		case tc.want == "":
			t.Errorf("[%s] %q → %v, want no skill", tc.lang, tc.msg, got)
		default:
			t.Errorf("[%s] %q → %v, want %q among them", tc.lang, tc.msg, got, tc.want)
		}
	}

	npos, nneg := countWanted(true), countWanted(false)
	t.Logf("recall@3 %d/%d positives, %d/%d negatives rejected, %.2f slugs returned on average",
		hit, npos, cleanNeg, nneg, float64(totalSlugs)/float64(len(skillCases)))
}

// TestRelatedSkills_Calibration prints the full score distribution so the
// thresholds above are measured rather than guessed. Diagnostic only.
func TestRelatedSkills_Calibration(t *testing.T) {
	if os.Getenv("CALIBRATE") == "" {
		t.Skip("set CALIBRATE=1")
	}
	cands := loadRealSkills(t)
	if _, ok := relatedSkillsEmbed("probe", cands); !ok {
		t.Skip("no local ollama embedding model")
	}

	for _, tc := range skillCases {
		top := topSkillScores(t, tc.msg, cands, 3)
		want := tc.want
		if want == "" {
			want = "(none)"
		}
		t.Logf("want=%-24s %-52s %s", want, truncRunes(tc.msg, 26), top)
	}
}

func topSkillScores(t *testing.T, msg string, cands []skillCandidate, k int) string {
	t.Helper()
	scores, bar, ok := rankSkills(msg, cands)
	if !ok {
		return "(unavailable)"
	}
	parts := []string{fmt.Sprintf("bar=%.3f", bar)}
	for i := 0; i < k && i < len(scores); i++ {
		parts = append(parts, fmt.Sprintf("%s=%.3f", scores[i].slug, scores[i].sim))
	}
	return strings.Join(parts, "  ")
}

func contains(xs []string, x string) bool {
	return slices.Contains(xs, x)
}

func countWanted(positive bool) int {
	n := 0
	for _, tc := range skillCases {
		if (tc.want != "") == positive {
			n++
		}
	}
	return n
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
