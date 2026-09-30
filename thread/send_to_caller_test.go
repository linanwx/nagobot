package thread

import (
	"context"
	"testing"
)

// newCallerTestThread builds a thread whose turn has both a channel destination
// and a separate caller sink — the shape every peer-woken turn has since
// CallerSink was split out of the turn's SinkSet.
func newCallerTestThread(sessionKey string) (*Thread, *[]string, *[]string) {
	var toChannel, toCaller []string
	t := &Thread{sessionKey: sessionKey}
	t.currentSink = NewSinks(SessionSink{
		Channel: "discord",
		Label:   "your response will be sent to discord channel 1474429571540582463",
		Send: func(_ context.Context, s string) error {
			toChannel = append(toChannel, s)
			return nil
		},
	})
	t.currentCallerSink = SessionSink{
		Label: "reply to caller session cron:weekday-noon-news-briefing via dispatch(to=caller:session)",
		Send: func(_ context.Context, s string) error {
			toCaller = append(toCaller, s)
			return nil
		},
	}
	t.lastWakeSource = WakeSession
	return t, &toChannel, &toCaller
}

// TestSendToCallerNeverSuppresses pins that replying to a caller leaves the
// turn's own destinations open, on every kind of session.
//
// The production regression it grew from: a cron dispatcher woke a Discord
// session, the turn wrote the noon briefing as content and acknowledged back
// with dispatch(to=caller:session), and the briefing was dropped because
// SendToCaller suppressed a sink that send never touched. Suppression later
// survived only for sessions with no human, because a subagent's default sink
// forwarded its content to the same parent the caller sink points at. That
// forwarding is gone, so there is no overlap left to guard on any session.
func TestSendToCallerNeverSuppresses(t *testing.T) {
	for _, key := range []string{
		"discord:1474429571540582463",
		"discord:1474429571540582463:threads:research",
		"cron:weekday-noon-news-briefing",
	} {
		th, _, toCaller := newCallerTestThread(key)
		if err := th.SendToCaller(context.Background(), "ack"); err != nil {
			t.Fatalf("%s: SendToCaller: %v", key, err)
		}
		if len(*toCaller) != 1 {
			t.Fatalf("%s: caller got %d messages, want 1", key, len(*toCaller))
		}
		if th.isSinkSuppressed() {
			t.Fatalf("%s: SendToCaller must not suppress the turn's own sinks", key)
		}
		if !th.hasDispatched() {
			t.Fatalf("%s: an executed caller reply must mark the turn as dispatched", key)
		}
	}
}
