package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/thread/msg"
)

type settleNoteHost struct {
	mockDispatchHost
	dest    string
	outcome msg.SettleOutcome
}

func (h *settleNoteHost) SettleTurnContent(context.Context, string, bool) (string, msg.SettleOutcome) {
	return h.dest, h.outcome
}

// TestSettleNoteIsSpecificToTheOutcome pins the note the MODEL reads.
//
// All four drop paths used to share one line: "the text in this message was NOT
// delivered to anyone — this turn has no reader for assistant content … put it
// in a send body". That was true only for SettleNoReader. On a batched dispatch
// the turn keeps running and the final message speaks; on a suppressed sink the
// reader already got the news in the send's own body. Telling the model its
// words vanished in those cases is an instruction to say everything twice.
func TestSettleNoteIsSpecificToTheOutcome(t *testing.T) {
	tests := []struct {
		name        string
		dest        string
		outcome     msg.SettleOutcome
		wantContain string
		wantAbsent  []string
	}{
		{
			name:        "delivered",
			dest:        "discord channel 1474429571540582463",
			wantContain: "was delivered — discord channel",
			wantAbsent:  []string{"⚠️", "send body"},
		},
		{
			name:        "no reader",
			outcome:     msg.SettleNoReader,
			wantContain: "reached nobody",
		},
		{
			name:        "turn continues",
			outcome:     msg.SettleTurnContinues,
			wantContain: "No need to repeat it",
			// The turn is not over and nothing was lost; a warning sign and
			// "put it in a send body" would both be wrong.
			wantAbsent: []string{"⚠️", "reached nobody"},
		},
		{
			name:        "already sent to caller",
			outcome:     msg.SettleAlreadySentToCaller,
			wantContain: "they already have it",
			wantAbsent:  []string{"⚠️", "reached nobody"},
		},
		{
			name:        "delivery failed",
			outcome:     msg.SettleDeliveryFailed,
			wantContain: "FAILED",
			wantAbsent:  []string{"reached nobody"},
		},
		{
			// The one that used to be reported as a SUCCESS. A drop sink's Send
			// returns nil, so the settle path read it as a real delivery and
			// spliced "Your message text was delivered — cron session — caller
			// output is dropped." into the tool result. The note must now say the
			// text was thrown away, and must not claim delivery.
			name:        "discarded",
			outcome:     msg.SettleDiscarded,
			wantContain: "DISCARDED",
			wantAbsent:  []string{"was delivered"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &DispatchTool{host: &settleNoteHost{dest: tt.dest, outcome: tt.outcome}}
			note := tool.settleContent(context.Background(), "some assistant prose", true)
			if !strings.Contains(note, tt.wantContain) {
				t.Fatalf("note = %q, want it to contain %q", note, tt.wantContain)
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(note, absent) {
					t.Fatalf("note = %q, must not contain %q", note, absent)
				}
			}
		})
	}
}

// TestSettleNoteEmptyWhenNothingToSay keeps the silent path silent: no content,
// or the runner already delivered it live (dest and outcome both zero).
func TestSettleNoteEmptyWhenNothingToSay(t *testing.T) {
	tool := &DispatchTool{host: &settleNoteHost{}}
	if note := tool.settleContent(context.Background(), "", true); note != "" {
		t.Fatalf("empty content produced note %q", note)
	}
	if note := tool.settleContent(context.Background(), "prose", true); note != "" {
		t.Fatalf("already-delivered content produced note %q", note)
	}
}
