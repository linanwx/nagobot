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
func wakeSink() SinkSet {
	return NewSinks(SessionSink{Label: "wake", Send: func(context.Context, string) error { return nil }})
}

// TestContentSinkRouting pins the delivery policy that replaced dispatch(to=user):
// the destination of plain content is decided by the wake source and the
// session, never by the model.
func TestContentSinkRouting(t *testing.T) {
	def := NewSinks(SessionSink{Label: "channel", Send: func(context.Context, string) error { return nil }})

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
		// The pin sibling's output is a one-line report of what it filed; the
		// file on disk is the actual result. Routed to defaultSink it would
		// push "created pins/foo.md" to the channel user after every pin.
		{"pin sibling keeps wake sink", "web:abc" + session.PinSessionSuffix, WakePin, "wake", false},
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
					t.Fatalf("contentSink = %q, want zero sink (silent)", got.Label())
				}
				return
			}
			if got.Label() != tc.want {
				t.Fatalf("contentSink = %q, want %q", got.Label(), tc.want)
			}
		})
	}
}

// TestDeliveryLabelMatchesContentSink pins the wake payload's `delivery` field to
// the destination contentSink actually picked, for every case where the two used
// to disagree.
//
// `delivery` is the only place a turn is told where its output goes
// (how-nagobot-works.md), but it was read straight off the wake sink's label —
// resolved when the wake was built, before contentSink got a say. So on a
// user-facing session woken by a peer, `delivery` named the caller
// ("reply to caller session … via dispatch(to=caller:session)") while plain
// content went to the channel user, contradicting the wake's own action hint in
// the same frontmatter block.
func TestDeliveryLabelMatchesContentSink(t *testing.T) {
	const channel = "your response will be sent to the web client for session web:a7a8bbb9"
	def := NewSinks(SessionSink{Label: channel, Send: func(context.Context, string) error { return nil }})

	// The wake sink a subagent's completion attaches to its parent — the exact
	// shape reported in the wild.
	childKey := "web:a7a8bbb9" + session.ThreadsSessionInfix + "best-chili-sauce"
	pairedToChild := NewSinks(SessionSink{
		Label: "reply to caller session " + childKey + " via dispatch(to=caller:session)",
		Send:  func(context.Context, string) error { return nil },
	})

	cases := []struct {
		name       string
		sessionKey string
		source     WakeSource
		wakeSink   SinkSet
		want       string
	}{
		// The regression: our own subagent reports back, we are user-facing, so
		// the answer reaches the human who has been waiting — not the child.
		{"own child reporting back names the human", "web:a7a8bbb9", WakeSession, pairedToChild, channel},
		// Same override for the other two proactive sources.
		{"cron names the human", "web:a7a8bbb9", WakeCron, wakeSink(), channel},
		{"progress names the human", "web:a7a8bbb9", WakeProgress, wakeSink(), channel},
		// Maintenance reaches nobody, and must say so rather than fall through
		// to whichever sink the scheduler happened to attach.
		{"heartbeat names nobody", "web:a7a8bbb9", WakeHeartbeat, wakeSink(), noDeliveryLabel},
		{"compression names nobody", "web:a7a8bbb9", WakeCompression, wakeSink(), noDeliveryLabel},
		// Unchanged: the human just spoke, so the wake's own sink is the target.
		{"user message names the wake sink", "web:a7a8bbb9", WakeWeb, wakeSink(), "wake"},
		// Unchanged: no human of its own, content stays on the wake sink — and
		// there the paired label is the truth, which is why it is kept.
		{"subagent names its caller", childKey, WakeSession, pairedToChild, pairedToChild.Label()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := &Thread{sessionKey: tc.sessionKey, defaultSink: def, lastWakeSource: tc.source}
			if got := th.resolveDeliveryLabel(tc.wakeSink); got != tc.want {
				t.Fatalf("delivery = %q, want %q", got, tc.want)
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
