package thread

import (
	"strings"
	"testing"
	"time"
)

// The whole local pre-think, end to end: real Ollama, the real skill pool, the
// real concurrency and the real budget. Everything else in this package tests one
// detector at a time against a fixed input; this is the only place the pieces have
// to work together under a clock.
//
// It skips without a local Ollama, because without one it would be testing the
// fallback path, which TestLocalPreThink_NoOllama already covers on purpose.
func TestLocalPreThink_EndToEnd(t *testing.T) {
	cands := loadRealSkills(t)
	if _, ok := relatedSkillsEmbed("probe", cands); !ok {
		t.Skip("no local ollama embedding model")
	}

	tests := []struct {
		name     string
		msg      string
		chat     string
		wantAll  []string
		wantNone []string
	}{
		{
			name:    "destructive request is flagged for confirmation",
			msg:     "帮我把 workspace/logs 目录下的旧日志文件都删了",
			wantAll: []string{"Destructive action:"},
		},
		{
			name:    "a bare confirmation inherits the danger of what it confirms",
			msg:     "执行吧",
			chat:    "[10:00] assistant: 我打算删除 workspace/logs 下 30 天前的 42 个日志文件，确认吗？",
			wantAll: []string{"Destructive action:"},
		},
		{
			name:     "the same words inherit nothing when the proposal was harmless",
			msg:      "执行吧",
			chat:     "[10:00] assistant: 我可以帮你把这篇文章总结成三点，需要吗？",
			wantNone: []string{"Destructive action:"},
		},
		{
			name:    "an explicit investigation request dispatches an investigator",
			msg:     "查一下今年显卡的价格走势",
			wantAll: []string{"Investigator:"},
		},
		{
			name:    "a link routes to the browser, not to a search",
			msg:     "看看这个 https://example.com/post/123 讲了什么",
			wantAll: []string{"Web URL present:"},
			// A URL is a FETCH task; dispatching a search for it is the redundancy
			// needsSearch's structural gate exists to prevent.
			wantNone: []string{"Search:"},
		},
		{
			name:    "a capability request retrieves the skill that handles it",
			msg:     "每天早上八点提醒我喝水",
			wantAll: []string{"Related skill", "manage-cron"},
		},
		{
			// The old prompt always said SOMETHING — a tone, a padded skill list. Silence
			// is now a real answer, and it is the common one.
			name:     "small talk produces no hint at all",
			msg:      "你好，在吗",
			wantNone: []string{"Destructive", "Search", "Investigator", "Web URL", "skill"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hint, took := localPreThink(tc.msg, tc.chat, cands)
			for _, w := range tc.wantAll {
				if !strings.Contains(hint, w) {
					t.Errorf("hint missing %q\ngot: %s", w, hint)
				}
			}
			for _, w := range tc.wantNone {
				if strings.Contains(hint, w) {
					t.Errorf("hint should not contain %q\ngot: %s", w, hint)
				}
			}
			if tc.name == "small talk produces no hint at all" && hint != "" {
				t.Errorf("expected silence, got: %q", hint)
			}
			if took > preThinkBudget {
				t.Errorf("took %v, over the %v budget", took, preThinkBudget)
			}
			t.Logf("%v — %s", took.Round(time.Millisecond), truncRunes(hint, 60))
		})
	}
}

// TestLocalPreThink_Latency is the number that justifies the whole exercise: the
// call it replaced blocked the user's turn for up to ten seconds.
//
// The first call pays for building three anchor indexes; every call after that is
// three concurrent embeddings of one short message over localhost.
func TestLocalPreThink_Latency(t *testing.T) {
	cands := loadRealSkills(t)
	if _, ok := relatedSkillsEmbed("probe", cands); !ok {
		t.Skip("no local ollama embedding model")
	}

	// The indexes are package-level and any earlier test in this process has already
	// built them, so a "cold" number measured without this reset is just a warm one
	// wearing a label. Drop them on the floor and time the real build — that figure
	// is the entire justification for WarmLocalPreThink.
	resetPreThinkIndexes()
	coldStart := time.Now()
	if !warmPreThinkIndexes(cands) {
		t.Skip("no local ollama embedding model")
	}
	t.Logf("cold: %v to build all three indexes — paid at daemon start by WarmLocalPreThink, not by a user",
		time.Since(coldStart).Round(time.Millisecond))

	msgs := []string{
		"帮我把这些旧文件删掉",
		"查一下今天的金价",
		"每天早上八点提醒我喝水",
		"你好",
		"explain how a bloom filter works",
	}
	var total time.Duration
	worst := time.Duration(0)
	for _, m := range msgs {
		_, took := localPreThink(m, "", cands)
		total += took
		if took > worst {
			worst = took
		}
	}
	avg := total / time.Duration(len(msgs))
	t.Logf("warm: avg %v, worst %v over %d messages (the LLM call this replaces had a %v timeout)",
		avg.Round(time.Millisecond), worst.Round(time.Millisecond), len(msgs), 10*time.Second)

	if worst > preThinkBudget {
		t.Errorf("worst case %v exceeds the budget %v", worst, preThinkBudget)
	}
}

// TestLocalPreThink_NoOllama pins the degradation. Without an embedding backend the
// analysis still runs — it just falls back to the regex verdicts, which is a weaker
// answer rather than no answer. The one thing that must NOT happen is a hang: the
// classifiers report unavailable rather than waiting.
func TestLocalPreThink_NoOllama(t *testing.T) {
	origD, origS := classifyDestructiveEmbedFn, classifySearchEmbedFn
	classifyDestructiveEmbedFn = func(string) (bool, bool) { return false, false }
	classifySearchEmbedFn = func(string) (bool, bool) { return false, false }
	defer func() {
		classifyDestructiveEmbedFn, classifySearchEmbedFn = origD, origS
	}()

	// The verb table still knows this one, and an explicit search request is pure
	// regex, so neither depends on the embedding layer.
	hint, took := localPreThink("帮我把 workspace/logs 下的旧日志删了", "", nil)
	if !strings.Contains(hint, "Destructive action:") {
		t.Errorf("regex-only path lost a destructive request it knows: %q", hint)
	}
	if took > 100*time.Millisecond {
		t.Errorf("regex-only path took %v — it should touch nothing and cost nothing", took)
	}
}

// resetPreThinkIndexes drops every cached anchor set and skill index so the next
// call rebuilds them from scratch. lastTry has to go too: it is a retry cooldown,
// and leaving it set would make the rebuild silently decline and report itself
// unavailable instead.
func resetPreThinkIndexes() {
	searchEmbed.mu.Lock()
	searchEmbed.model, searchEmbed.pos, searchEmbed.neg, searchEmbed.lastTry = "", nil, nil, time.Time{}
	searchEmbed.mu.Unlock()

	destructiveEmbed.mu.Lock()
	destructiveEmbed.model, destructiveEmbed.pos, destructiveEmbed.neg, destructiveEmbed.lastTry = "", nil, nil, time.Time{}
	destructiveEmbed.mu.Unlock()

	skillIndex.mu.Lock()
	skillIndex.key, skillIndex.groups, skillIndex.noneVecs, skillIndex.lastTry = "", nil, nil, time.Time{}
	skillIndex.mu.Unlock()
}
