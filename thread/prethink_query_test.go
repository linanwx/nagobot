package thread

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

var errNoTestBackend = errors.New("no backend (test)")

// fakeEmbed records every batch it is asked for and returns deterministic
// vectors. The batches are the whole point: this file exists to assert how many
// there are.
type fakeEmbed struct {
	mu      sync.Mutex
	batches [][]string
	err     error
}

func (f *fakeEmbed) fn(_ context.Context, texts []string) ([][]float64, error) {
	f.mu.Lock()
	f.batches = append(f.batches, append([]string{}, texts...))
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = []float64{float64(len(t)), 1, 0}
	}
	return out, nil
}

func (f *fakeEmbed) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

// TestQueryEmbedderServesPrefetchedTextsWithoutCalling is the property the
// rewrite buys: N classifiers, one round trip.
func TestQueryEmbedderServesPrefetchedTextsWithoutCalling(t *testing.T) {
	f := &fakeEmbed{}
	e := newQueryEmbedder(f.fn)
	ctx := context.Background()

	if err := e.prefetch(ctx, "alpha", "beta"); err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	for _, text := range []string{"alpha", "beta", "alpha", "beta"} {
		if _, err := e.vec(ctx, text); err != nil {
			t.Fatalf("vec(%q): %v", text, err)
		}
	}
	if got := e.roundTrips(); got != 1 {
		t.Fatalf("round trips = %d, want 1; batches=%v", got, f.batches)
	}
}

// TestQueryEmbedderDedupesWithinOneBatch: the classifiers pass the SAME string
// because they are asking the same question, and paying for it twice would
// undo half the saving.
func TestQueryEmbedderDedupesWithinOneBatch(t *testing.T) {
	f := &fakeEmbed{}
	e := newQueryEmbedder(f.fn)
	if err := e.prefetch(context.Background(), "same", "same", "", "other"); err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if len(f.batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(f.batches))
	}
	if len(f.batches[0]) != 2 {
		t.Fatalf("batch inputs = %v, want the duplicate and the empty string dropped", f.batches[0])
	}
}

// TestQueryEmbedderRemembersAFailedPrefetch: one dead round trip must not turn
// into one per classifier, against a backend that is already struggling.
func TestQueryEmbedderRemembersAFailedPrefetch(t *testing.T) {
	f := &fakeEmbed{err: errNoTestBackend}
	e := newQueryEmbedder(f.fn)
	ctx := context.Background()

	if err := e.prefetch(ctx, "alpha"); err == nil {
		t.Fatal("prefetch should have failed")
	}
	for i := 0; i < 4; i++ {
		if _, err := e.vec(ctx, "alpha"); !errors.Is(err, errNoTestBackend) {
			t.Fatalf("vec after failed prefetch: %v", err)
		}
	}
	if got := e.roundTrips(); got != 1 {
		t.Fatalf("round trips = %d, want 1 — the failure was retried", got)
	}
}

// TestQueryEmbedderEmbedsUnprefetchedOnDemand keeps the fallback honest: a text
// nobody predicted is still answered, just not for free.
func TestQueryEmbedderEmbedsUnprefetchedOnDemand(t *testing.T) {
	f := &fakeEmbed{}
	e := newQueryEmbedder(f.fn)
	ctx := context.Background()
	_ = e.prefetch(ctx, "alpha")
	if _, err := e.vec(ctx, "surprise"); err != nil {
		t.Fatalf("vec: %v", err)
	}
	if got := e.roundTrips(); got != 2 {
		t.Fatalf("round trips = %d, want 2", got)
	}
}

// TestQueryVectorFallsBackWithoutAnEmbedderOnContext covers the direct callers
// — the per-classifier tests and WarmLocalPreThink — which have no shared
// embedder and must still work.
func TestQueryVectorFallsBackWithoutAnEmbedderOnContext(t *testing.T) {
	f := &fakeEmbed{}
	if _, err := queryVector(context.Background(), f.fn, "solo"); err != nil {
		t.Fatalf("queryVector: %v", err)
	}
	if f.calls() != 1 {
		t.Fatalf("calls = %d, want 1", f.calls())
	}
}

// TestLocalPreThinkMakesOneEmbeddingRoundTrip is the end-to-end statement of
// the whole rewrite. It fails if a classifier asks for a query text nobody
// prefetched — a different instruction, a different truncation, a different
// subject — because that lands on the on-demand path and costs a second round
// trip. That drift is the failure mode: three classifiers had already drifted
// INTO agreement by accident, and nothing would have noticed them drifting out.
//
// The real skill pool is used so all four classifiers actually run; with no
// backend configured their indexes never build and only the prefetch is
// counted, which still pins the ceiling.
func TestLocalPreThinkMakesOneEmbeddingRoundTrip(t *testing.T) {
	cands := loadRealSkills(t)
	if _, ok := relatedSkillsEmbed(context.Background(), "probe", cands); !ok {
		t.Log("no embedding backend — asserting the ceiling only, classifiers will report unavailable")
	}

	f := &fakeEmbed{}
	orig := preThinkEmbedFn
	preThinkEmbedFn = func() embedFn { return f.fn }
	defer func() { preThinkEmbedFn = orig }()

	for _, msg := range []string{
		"帮我写个脚本把旧日志删掉",
		"can you look up the current price of copper",
		"谢谢你",
	} {
		f.mu.Lock()
		f.batches = nil
		f.mu.Unlock()
		localPreThink(context.Background(), msg, "", cands)
		if got := f.calls(); got > 1 {
			t.Fatalf("%q made %d embedding round trips, want at most 1; batches=%v", msg, got, f.batches)
		}
	}
}

// TestLocalPreThinkPrefetchesTheConfirmationAntecedent: a bare confirmation is
// the one message where destructive judges different text than everyone else,
// so the prefetch carries two inputs — still one round trip.
func TestLocalPreThinkPrefetchesTheConfirmationAntecedent(t *testing.T) {
	f := &fakeEmbed{}
	orig := preThinkEmbedFn
	preThinkEmbedFn = func() embedFn { return f.fn }
	defer func() { preThinkEmbedFn = orig }()

	const chat = "user: 要不要把旧分支清掉\nassistant: 我可以把 20 个已合并的分支删掉，确认吗"
	localPreThink(context.Background(), "执行吧", chat, nil)

	if f.calls() != 1 {
		t.Fatalf("round trips = %d, want 1; batches=%v", f.calls(), f.batches)
	}
	if n := len(f.batches[0]); n != 2 {
		t.Fatalf("prefetch carried %d inputs, want 2 (the confirmation and what it confirms): %v", n, f.batches[0])
	}
}

// TestPreThinkQueryIsOneInstruction pins the collapse itself. These were three
// separately declared constants holding the same sentence, which is how the
// classifiers ended up issuing identical requests without anyone noticing.
func TestPreThinkQueryIsOneInstruction(t *testing.T) {
	if searchEmbedTask != preThinkQueryTask || destructiveEmbedTask != preThinkQueryTask {
		t.Fatal("an anchor task string has drifted from preThinkQueryTask — the query vector no longer matches the anchors it is scored against")
	}
}

// TestPreThinkQueryTruncatesOnce: one vector admits one truncation, and it is
// the length destructive and coder were calibrated at.
func TestPreThinkQueryTruncatesOnce(t *testing.T) {
	long := strings.Repeat("删", preThinkQueryMaxRunes+50)
	q := preThinkQuery("qwen/qwen3-embedding-4b", long)
	body := strings.TrimPrefix(q, "Instruct: "+preThinkQueryTask+"\nQuery: ")
	if body == q {
		t.Fatal("qwen3 model did not get the instruction prefix")
	}
	if n := len([]rune(body)); n != preThinkQueryMaxRunes {
		t.Fatalf("truncated to %d runes, want %d", n, preThinkQueryMaxRunes)
	}
	if raw := preThinkQuery("some/other-embedding", "hi"); raw != "hi" {
		t.Fatalf("non-qwen3 model got a prefix it was not trained on: %q", raw)
	}
}

// TestDestructiveSubjectIsWhatGetsPrefetched: localPreThink prefetches
// destructiveSubject's answer, and the classifier embeds it. Two definitions
// would mean a silent extra round trip on every bare confirmation.
func TestDestructiveSubjectIsWhatGetsPrefetched(t *testing.T) {
	const chat = "user: 要不要把旧分支清掉\nassistant: 我可以把 20 个已合并的分支删掉，确认吗"
	if got := destructiveSubject("执行吧", chat); !strings.Contains(got, "删掉") {
		t.Fatalf("a bare confirmation must be judged by what it confirms, got %q", got)
	}
	if got := destructiveSubject("帮我删掉旧日志", chat); got != "帮我删掉旧日志" {
		t.Fatalf("an ordinary message must be judged on its own, got %q", got)
	}
}
