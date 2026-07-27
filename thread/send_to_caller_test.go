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

// TestSendToCallerKeepsUserFacingContent is the production regression.
//
// A cron dispatcher woke a Discord session; the wake's own `delivery` field
// promised "your response will be sent to discord channel …". The turn wrote the
// noon briefing as content and acknowledged back with dispatch(to=caller:session).
// SendToCaller suppressed the whole SinkSet, so SettleTurnContent dropped the
// briefing with reason "sink already used by an executed send" — a sink that send
// never touched, since the caller lives on CallerSink and left the set when the
// two were separated.
func TestSendToCallerKeepsUserFacingContent(t *testing.T) {
	th, toChannel, toCaller := newCallerTestThread("discord:1474429571540582463")

	if err := th.SendToCaller(context.Background(), "已精简转发 ✅"); err != nil {
		t.Fatalf("SendToCaller: %v", err)
	}
	if len(*toCaller) != 1 {
		t.Fatalf("caller got %d messages, want 1", len(*toCaller))
	}
	if th.isSinkSuppressed() {
		t.Fatal("a user-facing session's channel must stay open: content and the caller reply are two audiences")
	}

	dest, outcome := th.SettleTurnContent(context.Background(), "📰 周一午间简报｜7月27日 …", true)
	if outcome != "" {
		t.Fatal("content promised to the discord channel was dropped")
	}
	if dest == "" || len(*toChannel) != 1 {
		t.Fatalf("content not delivered to the channel: dest=%q sent=%v", dest, *toChannel)
	}
}

// TestSendToCallerSuppressesForSubagent keeps the case suppression exists for.
// A subagent has no human: contentSink routes plain content to its default sink,
// which forwards to the parent — the same reader the caller sink points at. Both
// speaking wakes the parent twice.
func TestSendToCallerSuppressesForSubagent(t *testing.T) {
	th, _, toCaller := newCallerTestThread("discord:1474429571540582463:threads:research")

	if err := th.SendToCaller(context.Background(), "done"); err != nil {
		t.Fatalf("SendToCaller: %v", err)
	}
	if len(*toCaller) != 1 {
		t.Fatalf("caller got %d messages, want 1", len(*toCaller))
	}
	if !th.isSinkSuppressed() {
		t.Fatal("a subagent's content sink forwards to the same parent — it must be suppressed")
	}

	if _, outcome := th.SettleTurnContent(context.Background(), "some prose about the work", true); outcome != SettleAlreadySentToCaller {
		t.Fatalf("subagent content outcome = %q, want %q", outcome, SettleAlreadySentToCaller)
	}
}

// TestSendToCallerSuppressesForCronSession covers the other non-user-facing
// shape: a cron session's own output is dropped by design, so suppression there
// changes nothing and must not regress into "user-facing".
func TestSendToCallerSuppressesForCronSession(t *testing.T) {
	th, _, _ := newCallerTestThread("cron:weekday-noon-news-briefing")

	if err := th.SendToCaller(context.Background(), "ack"); err != nil {
		t.Fatalf("SendToCaller: %v", err)
	}
	if !th.isSinkSuppressed() {
		t.Fatal("a cron session is not user-facing; suppression should still apply")
	}
}
