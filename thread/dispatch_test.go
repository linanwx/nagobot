package thread

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/session"
)

// TestSendToUserRecordsChat verifies that proactive to=user delivery — which
// goes through defaultSink and bypasses the per-wake chat.jsonl sink — still
// records an assistant entry into the clean chat log. Without this, bot-initiated
// messages (heartbeat greetings, cron follow-ups) would be invisible to
// pre-think's recent-chat context.
func TestSendToUserRecordsChat(t *testing.T) {
	sessMgr, err := session.NewManager(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	mgr := NewManager(&ThreadConfig{Sessions: sessMgr})

	var delivered string
	th := &Thread{
		sessionKey: "cli", // user-facing per IsUserFacing
		mgr:        mgr,
		defaultSink: Sink{
			Send: func(_ context.Context, body string) error { delivered = body; return nil },
		},
	}

	if err := th.SendToUser(context.Background(), "proactive hello"); err != nil {
		t.Fatalf("SendToUser: %v", err)
	}
	if delivered != "proactive hello" {
		t.Errorf("delivered = %q, want %q", delivered, "proactive hello")
	}

	got := session.ReadRecentChat(mgr.SessionDir("cli"), 5, time.Local)
	if !strings.Contains(got, "assistant: proactive hello") {
		t.Errorf("chat.jsonl missing assistant entry; got %q", got)
	}
}

// TestSendToUserSkipsChatOnDeliveryFailure ensures a failed channel delivery is
// not recorded — the clean chat log should reflect messages the user actually
// received, not ones that errored out.
func TestSendToUserSkipsChatOnDeliveryFailure(t *testing.T) {
	sessMgr, err := session.NewManager(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	mgr := NewManager(&ThreadConfig{Sessions: sessMgr})

	th := &Thread{
		sessionKey: "cli",
		mgr:        mgr,
		defaultSink: Sink{
			Send: func(_ context.Context, _ string) error { return context.Canceled },
		},
	}

	if err := th.SendToUser(context.Background(), "undelivered"); err == nil {
		t.Fatal("SendToUser should propagate the delivery error")
	}
	if got := session.ReadRecentChat(mgr.SessionDir("cli"), 5, time.Local); got != "" {
		t.Errorf("chat.jsonl should be empty after failed delivery; got %q", got)
	}
}
