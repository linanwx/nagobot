package thread

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The whole local pre-think, end to end: the real remote backend, the real skill pool, the
// real concurrency and the real budget. Everything else in this package tests one
// detector at a time against a fixed input; this is the only place the pieces have
// to work together under a clock.
//
// It skips without a configured backend, because without one it would be testing the
// fallback path, which TestLocalPreThink_NoBackend already covers on purpose.
func TestLocalPreThink_EndToEnd(t *testing.T) {
	cands := loadRealSkills(t)
	if _, ok := relatedSkillsEmbed(context.Background(), "probe", cands); !ok {
		t.Skip("no embedding backend configured")
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
			name:    "a code production request dispatches the coder",
			msg:     "帮我写一个 Python 脚本，把这个目录下的图片都压缩一遍",
			wantAll: []string{"Code task:", "coder subagent"},
		},
		{
			name:     "a concept question about code stays inline",
			msg:      "解释一下什么是依赖注入",
			wantNone: []string{"Code task:"},
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
			hint, took := localPreThink(context.Background(), tc.msg, tc.chat, cands)
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
// The first call pays for building four anchor indexes; every call after that is
// four concurrent embeddings of one short message over localhost.
func TestLocalPreThink_Latency(t *testing.T) {
	cands := loadRealSkills(t)
	if _, ok := relatedSkillsEmbed(context.Background(), "probe", cands); !ok {
		t.Skip("no embedding backend configured")
	}

	// The indexes are package-level and any earlier test in this process has already
	// built them, so a "cold" number measured without this reset is just a warm one
	// wearing a label. Drop them on the floor and time the real build — that figure
	// is the entire justification for WarmLocalPreThink.
	resetPreThinkIndexes()
	coldStart := time.Now()
	if !warmPreThinkIndexes(cands) {
		t.Skip("no embedding backend configured")
	}
	t.Logf("cold: %v to build all four indexes — paid at daemon start by WarmLocalPreThink, not by a user",
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
		_, took := localPreThink(context.Background(), m, "", cands)
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

// TestLocalPreThink_NoBackend pins the degradation. Without an embedding backend the
// analysis still runs — it just falls back to the regex verdicts, which is a weaker
// answer rather than no answer. The one thing that must NOT happen is a hang: the
// classifiers report unavailable rather than waiting.
func TestLocalPreThink_NoBackend(t *testing.T) {
	origD, origS, origC := classifyDestructiveEmbedFn, classifySearchEmbedFn, classifyCoderEmbedFn
	classifyDestructiveEmbedFn = func(context.Context, string) (bool, bool) { return false, false }
	classifySearchEmbedFn = func(context.Context, string) (bool, bool) { return false, false }
	classifyCoderEmbedFn = func(context.Context, string) (bool, bool) { return false, false }
	// The embedding round trip is now made ONCE, up front, by localPreThink
	// itself rather than by each classifier — so simulating "no backend" has to
	// stub it there too. On a developer machine that has a real key configured,
	// stubbing only the classify functions would leave the shared prefetch
	// talking to the network and this test would measure it.
	origEmbed := preThinkEmbedFn
	preThinkEmbedFn = func() embedFn {
		return func(context.Context, []string) ([][]float64, error) { return nil, errNoTestBackend }
	}
	defer func() {
		classifyDestructiveEmbedFn, classifySearchEmbedFn, classifyCoderEmbedFn = origD, origS, origC
		preThinkEmbedFn = origEmbed
	}()

	// The verb table still knows this one, and an explicit search request is pure
	// regex, so neither depends on the embedding layer.
	hint, took := localPreThink(context.Background(), "帮我把 workspace/logs 下的旧日志删了", "", nil)
	if !strings.Contains(hint, "Destructive action:") {
		t.Errorf("regex-only path lost a destructive request it knows: %q", hint)
	}
	if took > 100*time.Millisecond {
		t.Errorf("regex-only path took %v — it should touch nothing and cost nothing", took)
	}
}

// TestPreThinkBudget_CallerLeavesWithItsDeadline pins what the budget is actually
// worth. localPreThink gives up after preThinkBudget and answers from the regex
// verdicts — but giving up on the ANSWER is not the same as giving up on the WORK.
// The classifiers serialize on their indexes, and a cold build against a slow
// backend holds one for seconds; on a plain sync.Mutex every message arriving
// meanwhile parked a goroutine behind it, for a turn that was already over. Under
// sustained traffic that queue only grows.
//
// So a classifier whose caller has run out of time must leave, not wait. It then
// reports itself unavailable, which is exactly the "no backend" path — the
// regex verdict stands.
//
// No backend needed: contention is resolved before the index is ever touched.
func TestPreThinkBudget_CallerLeavesWithItsDeadline(t *testing.T) {
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { // stands in for a cold index build holding the lock
		skillIndex.mu.lock(context.Background())
		close(held)
		<-release
		skillIndex.mu.unlock()
		close(done)
	}()
	<-held
	defer func() { close(release); <-done }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, ok := relatedSkillsEmbed(ctx, "每天早上八点提醒我喝水",
		[]skillCandidate{{Slug: "manage-cron", Description: "create and manage scheduled jobs"}})
	took := time.Since(start)

	if ok {
		t.Error("a caller past its deadline must report unavailable, not produce an answer")
	}
	if took > 500*time.Millisecond {
		t.Errorf("waited %v on a 50ms budget — the caller is still parked on the index lock", took)
	}
}

// resetPreThinkIndexes drops every cached anchor set and skill index so the next
// call rebuilds them from scratch. lastTry has to go too: it is a retry cooldown,
// and leaving it set would make the rebuild silently decline and report itself
// unavailable instead.
func resetPreThinkIndexes() {
	ctx := context.Background() // no deadline: a reset that gives up would silently no-op

	searchEmbed.mu.lock(ctx)
	searchEmbed.model, searchEmbed.pos, searchEmbed.neg, searchEmbed.lastTry = "", nil, nil, time.Time{}
	searchEmbed.mu.unlock()

	destructiveEmbed.mu.lock(ctx)
	destructiveEmbed.model, destructiveEmbed.pos, destructiveEmbed.neg, destructiveEmbed.lastTry = "", nil, nil, time.Time{}
	destructiveEmbed.mu.unlock()

	coderEmbed.mu.lock(ctx)
	coderEmbed.model, coderEmbed.pos, coderEmbed.neg, coderEmbed.lastTry = "", nil, nil, time.Time{}
	coderEmbed.mu.unlock()

	skillIndex.mu.lock(ctx)
	skillIndex.key, skillIndex.groups, skillIndex.noneVecs, skillIndex.lastTry = "", nil, nil, time.Time{}
	skillIndex.mu.unlock()
}
