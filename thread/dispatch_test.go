package thread

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/session"
)

// wakeSink is a recognizable stand-in for the per-wake sink, so a test can tell
// which of the two sinks contentSink picked.
func wakeSink() Sink {
	return Sink{Label: "wake", Send: func(context.Context, string) error { return nil }}
}

// TestContentSinkRouting pins the delivery policy that replaced dispatch(to=user):
// the destination of plain content is decided by the wake source and the
// session, never by the model.
func TestContentSinkRouting(t *testing.T) {
	def := Sink{Label: "channel", Send: func(context.Context, string) error { return nil }}

	cases := []struct {
		name       string
		sessionKey string
		source     WakeSource
		want       string // sink Label, "" means zero sink (silent)
		proactive  bool   // bypasses the wake sink's chat.jsonl write
	}{
		// The human just spoke: reply on the wake's own sink, which carries
		// per-wake context (stream binding, chat.jsonl, reply threading).
		{"user message keeps wake sink", "cli", WakeWeb, "wake", false},
		// Proactive sources on a user-facing session now reach the human by
		// simply producing content — this is what to=user used to do.
		{"cron speaks to the human", "cli", WakeCron, "channel", true},
		{"peer session speaks to the human", "cli", WakeSession, "channel", true},
		{"progress speaks to the human", "cli", WakeProgress, "channel", true},
		// Maintenance must never speak, independent of what the model writes
		// and of whichever sink the scheduler attached.
		{"heartbeat is silent", "cli", WakeHeartbeat, "", false},
		{"legacy heartbeat source is silent", "cli", WakeSource("heartbeat_reflect"), "", false},
		{"compression is silent", "cli", WakeCompression, "", false},
		// No human of its own: content stays on the wake sink and the runner
		// separately requires an explicit dispatch.
		{"subagent keeps wake sink", "cli:threads:job", WakeSession, "wake", false},
		{"cron session keeps wake sink", "cron:tidyup", WakeCron, "wake", false},
		// Internal sibling sessions ({key}:quote, :imagepreview, …) hang off a
		// user-facing key but have no human — their whole output is a value
		// returned to the caller via OnComplete. Routing it to defaultSink
		// pushes an internal artifact to the channel user (on web, to every
		// enrolled device, since no page is bound to the sibling key).
		{"quote sibling keeps wake sink", "web:abc" + session.QuoteSessionSuffix, WakeQuote, "wake", false},
		{"image preview sibling keeps wake sink", "web:abc" + session.ImagePreviewSessionSuffix, WakeImagePreview, "wake", false},
		{"audio preview sibling keeps wake sink", "telegram:123" + session.AudioPreviewSessionSuffix, WakeAudioPreview, "wake", false},
		{"progress summary sibling keeps wake sink", "web:abc" + session.ProgressSummarySessionSuffix, WakeProgressSum, "wake", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := &Thread{sessionKey: tc.sessionKey, defaultSink: def, lastWakeSource: tc.source}
			got, proactive := th.contentSink(wakeSink())
			if proactive != tc.proactive {
				t.Errorf("proactive = %v, want %v", proactive, tc.proactive)
			}
			if tc.want == "" {
				if !got.IsZero() {
					t.Fatalf("contentSink = %q, want zero sink (silent)", got.Label)
				}
				return
			}
			if got.Label != tc.want {
				t.Fatalf("contentSink = %q, want %q", got.Label, tc.want)
			}
		})
	}
}

// TestRecordProactiveChatWritesChatLog verifies bot-initiated content is written
// to the clean chat log. It is delivered on the channel sink, which bypasses the
// per-wake chat.jsonl sink — without this, cron follow-ups and progress notes
// would be invisible to pre-think's recent-chat context.
func TestRecordProactiveChatWritesChatLog(t *testing.T) {
	sessMgr, err := session.NewManager(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	mgr := NewManager(&ThreadConfig{Sessions: sessMgr})

	th := &Thread{sessionKey: "cli", mgr: mgr, lastWakeSource: WakeCron}
	th.recordProactiveChat("proactive hello")

	got := session.ReadRecentChat(mgr.SessionDir("cli"), 5, time.Local)
	if !strings.Contains(got, "assistant: proactive hello") {
		t.Errorf("chat.jsonl missing assistant entry; got %q", got)
	}
	// The rendered form omits the origin, so assert it on the raw record: it is
	// what lets a reader tell a bot-initiated message from a plain reply.
	raw, err := os.ReadFile(filepath.Join(mgr.SessionDir("cli"), "chat.jsonl"))
	if err != nil {
		t.Fatalf("read chat.jsonl: %v", err)
	}
	if !strings.Contains(string(raw), string(WakeCron)) {
		t.Errorf("chat.jsonl should record the driving source %q; got %s", WakeCron, raw)
	}
}
