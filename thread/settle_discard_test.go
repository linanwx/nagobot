package thread

import (
	"context"
	"testing"
)

// TestDropSinkIsNotReportedAsDelivered is the production regression.
//
// A cron session's default sink accepts the message and writes one Debug line.
// Its Send returns nil like every other sink's, so the settle path could not
// tell it apart from a real delivery: it reported success and dispatch spliced
// "Your message text was delivered — cron session — caller output is dropped."
// into the tool result. The model was told its prose reached someone when it had
// reached a log line nobody reads at the default level.
func TestDropSinkIsNotReportedAsDelivered(t *testing.T) {
	var dropped []string
	th := &Thread{sessionKey: "cron:weekly-bidding-report"}
	th.currentSink = NewSinks(SessionSink{
		Label:    "cron session — caller output is dropped. Use dispatch(to=session, ...) to deliver explicitly.",
		Discards: true,
		Send: func(_ context.Context, s string) error {
			dropped = append(dropped, s)
			return nil
		},
	})
	th.lastWakeSource = WakeCron

	dest, outcome := th.SettleTurnContent(context.Background(), "本周招投标共 12 条，重点 3 条 …", true)
	if outcome != SettleDiscarded {
		t.Fatalf("outcome = %q, want %q", outcome, SettleDiscarded)
	}
	if dest != "" {
		t.Fatalf("a discarding sink must not be reported as a destination, got %q", dest)
	}
	if len(dropped) != 0 {
		t.Fatalf("nothing should be handed to a sink that throws it away, got %v", dropped)
	}
}

// TestNoSinkAtAllStaysNoReader keeps the two "nobody read it" outcomes apart. A
// turn with no destination is a different fact from a turn whose only
// destination discards, and the notes the model reads differ.
func TestNoSinkAtAllStaysNoReader(t *testing.T) {
	th := &Thread{sessionKey: "discord:123"}
	th.lastWakeSource = WakeHeartbeat

	if _, outcome := th.SettleTurnContent(context.Background(), "maintenance prose", true); outcome != SettleNoReader {
		t.Fatalf("outcome = %q, want %q", outcome, SettleNoReader)
	}
}

// TestRealSinkAlongsideADropSinkStillDelivers guards the filter's other
// direction: removing the discarding member must not remove the live one. A cron
// session is observable, so a browser watching it contributes a real sink to the
// same set.
func TestRealSinkAlongsideADropSinkStillDelivers(t *testing.T) {
	var seen []string
	th := &Thread{sessionKey: "cron:weekly-bidding-report"}
	th.currentSink = NewSinks(
		SessionSink{
			Label:    "cron session — caller output is dropped.",
			Discards: true,
			Send:     func(context.Context, string) error { return nil },
		},
		SessionSink{
			Label: "your response will be sent to the web client",
			Send: func(_ context.Context, s string) error {
				seen = append(seen, s)
				return nil
			},
		},
	)
	th.lastWakeSource = WakeCron

	dest, outcome := th.SettleTurnContent(context.Background(), "本周招投标共 12 条 …", true)
	if outcome != "" {
		t.Fatalf("outcome = %q, want delivery", outcome)
	}
	if len(seen) != 1 {
		t.Fatalf("the real destination got %d messages, want 1", len(seen))
	}
	if dest != "your response will be sent to the web client" {
		t.Fatalf("dest = %q — the drop sink's label must not appear in it", dest)
	}
}
