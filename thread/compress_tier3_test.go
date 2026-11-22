package thread

import (
	"strings"
	"testing"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
)

// newTier3TestManager builds a Manager backed by a temp session store with the
// given context window, plus an idle thread for key.
func newTier3TestManager(t *testing.T, key string, window int) (*Manager, *Thread, *session.Manager) {
	t.Helper()
	store, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	mgr := NewManager(&ThreadConfig{Sessions: store, ContextWindowTokens: window})
	th, err := mgr.NewThread(key, "")
	if err != nil {
		t.Fatalf("NewThread: %v", err)
	}
	return mgr, th, store
}

func TestTryTier3Compress_EnqueuesWhenOverThreshold(t *testing.T) {
	key := "test:tier3-over"
	// window 2000 → WarnToken 300 (15%) → Tier 3 threshold = 1700 tokens.
	mgr, th, store := newTier3TestManager(t, key, 2000)

	// A session far over the threshold.
	big := strings.Repeat("alpha beta gamma delta epsilon ", 800)
	if err := store.Append(key, provider.UserMessage(big)); err != nil {
		t.Fatalf("append: %v", err)
	}

	mgr.tryTier3Compress(key)

	if !th.hasMessages() {
		t.Fatal("expected a compression wake to be enqueued for an over-threshold session")
	}
	wake := <-th.inbox
	if wake.Source != WakeCompression {
		t.Errorf("enqueued wake source = %q, want %q", wake.Source, WakeCompression)
	}
}

func TestTryTier3Compress_SkipsWhenUnderThreshold(t *testing.T) {
	key := "test:tier3-under"
	// Large window → Tier 3 threshold 140000 tokens (70%, the default); a tiny session stays well under.
	mgr, th, store := newTier3TestManager(t, key, 200000)

	if err := store.Append(key, provider.UserMessage("just a short message")); err != nil {
		t.Fatalf("append: %v", err)
	}

	mgr.tryTier3Compress(key)

	if th.hasMessages() {
		t.Error("an under-threshold session must not enqueue a compression wake")
	}
}

// The trim sizes the whole request, system prompt included, so Tier 3 must too.
// A session whose messages alone sit under the threshold but whose system
// prompt pushes the request over it has to compress; otherwise it parks at the
// trim line and the trim flips the request head on and off, missing the cache.
func TestTryTier3Compress_CountsTheSystemPrompt(t *testing.T) {
	key := "test:tier3-system-prompt"
	// window 4000 → Tier 3 threshold = 2800 tokens (70%, the default).
	mgr, th, store := newTier3TestManager(t, key, 4000)
	if err := store.Append(key, provider.UserMessage(strings.Repeat("alpha beta ", 100))); err != nil {
		t.Fatalf("append: %v", err)
	}

	mgr.tryTier3Compress(key)
	if th.hasMessages() {
		t.Fatal("messages alone are under the threshold; nothing should be enqueued yet")
	}

	th.lastSystemPromptTokens = 2800
	mgr.tryTier3Compress(key)
	if !th.hasMessages() {
		t.Fatal("system prompt + messages exceed the threshold; a compression wake must be enqueued")
	}
}

// ContextUsage reports exactly the inputs tryAICompress sizes the request
// with, so the web ring and the tiers cannot disagree about one session.
func TestContextUsageMatchesTheTierInputs(t *testing.T) {
	key := "test:context-usage"
	mgr, th, store := newTier3TestManager(t, key, 300000)
	if err := store.Append(key, provider.UserMessage(strings.Repeat("alpha beta ", 100))); err != nil {
		t.Fatalf("append: %v", err)
	}
	th.lastSystemPromptTokens = 4321

	u, ok := mgr.ContextUsage(key)
	if !ok {
		t.Fatal("no usage for a loaded thread")
	}
	if u.SystemPromptTokens != 4321 {
		t.Errorf("SystemPromptTokens = %d, want the last turn's 4321", u.SystemPromptTokens)
	}
	if want := EstimateToolDefsTokens(th.tools.Defs()); u.ToolDefsTokens != want {
		t.Errorf("ToolDefsTokens = %d, want %d", u.ToolDefsTokens, want)
	}
	if want := th.contextBudget().ContextWindow; u.ContextWindow != want {
		t.Errorf("ContextWindow = %d, want %d", u.ContextWindow, want)
	}
	if want := contextLoopBudget(u.ContextWindow, th.cfg().MaxCompletionTokens); u.TrimBudget != want {
		t.Errorf("TrimBudget = %d, want %d", u.TrimBudget, want)
	}
	if _, ok := mgr.ContextUsage("test:not-loaded"); ok {
		t.Error("usage reported for a session with no thread")
	}
}
