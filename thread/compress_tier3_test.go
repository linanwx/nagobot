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
	// window 2000 → WarnToken 400 → Tier 3 threshold = 1600 tokens.
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
	// Large window → Tier 3 threshold ~160000 tokens; a tiny session stays well under.
	mgr, th, store := newTier3TestManager(t, key, 200000)

	if err := store.Append(key, provider.UserMessage("just a short message")); err != nil {
		t.Fatalf("append: %v", err)
	}

	mgr.tryTier3Compress(key)

	if th.hasMessages() {
		t.Error("an under-threshold session must not enqueue a compression wake")
	}
}
