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

// A Tier 1 run resets the forced-Tier1 user-message counter (records the new
// position), even when nothing actually gets compressed — "this run counts".
func TestTryTier1Compress_ResetsUserMsgCounter(t *testing.T) {
	key := "test:tier1-reset"
	mgr, th, store := newTier3TestManager(t, key, 200000)
	if err := store.Append(key, provider.UserMessage("some session content")); err != nil {
		t.Fatalf("append: %v", err)
	}
	th.userMsgsSinceTier1 = forcedTier1UserMsgs // simulate 5 accumulated user messages

	mgr.tryTier1Compress(key)

	if th.userMsgsSinceTier1 != 0 {
		t.Errorf("Tier 1 should reset the user-message counter to 0, got %d", th.userMsgsSinceTier1)
	}
}

func TestTryTier3Compress_SkipsWhenUnderThreshold(t *testing.T) {
	key := "test:tier3-under"
	// Large window → Tier 3 threshold 170000 tokens (85%); a tiny session stays well under.
	mgr, th, store := newTier3TestManager(t, key, 200000)

	if err := store.Append(key, provider.UserMessage("just a short message")); err != nil {
		t.Fatalf("append: %v", err)
	}

	mgr.tryTier3Compress(key)

	if th.hasMessages() {
		t.Error("an under-threshold session must not enqueue a compression wake")
	}
}
