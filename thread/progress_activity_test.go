package thread

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

// The case that motivated the activity line: a child's last tool failed and
// it is now writing its answer. The tool trace alone reads as "stuck after an
// error"; the activity line has to say otherwise.
func TestDescribeActivity(t *testing.T) {
	now := time.Now()
	ago := func(s int) time.Time { return now.Add(-time.Duration(s) * time.Second) }
	cases := []struct {
		name string
		info msg.ThreadInfo
		want string
	}{
		{"between steps", msg.ThreadInfo{}, ""},
		{"tool", msg.ThreadInfo{CurrentTool: "exec", CurrentToolSince: ago(130)}, "running_tool exec · 2m10s"},
		{"no output yet", msg.ThreadInfo{LLMCallStart: ago(1)}, "waiting_first_output · model call 1s"},
		{"thinking", msg.ThreadInfo{LLMCallStart: ago(20), LastReasoningAt: ago(1)}, "thinking · model call 20s · last output 1s ago"},
		{"writing reply", msg.ThreadInfo{LLMCallStart: ago(60), LastReasoningAt: ago(30), LastTextDeltaAt: ago(2), ReplyRunes: 1830},
			"writing_reply · 1830 chars so far · model call 1m00s · last output 2s ago"},
		// A preamble followed by a tool call is not an answer.
		{"tool call after preamble", msg.ThreadInfo{LLMCallStart: ago(40), LastTextDeltaAt: ago(35), ReplyRunes: 70, ToolCallDraft: "write_file"},
			"writing_tool_call write_file · model call 40s"},
		// Text from an EARLIER call must not date this one's output.
		{"stale text stamp", msg.ThreadInfo{LLMCallStart: ago(5), LastTextDeltaAt: ago(90), LastReasoningAt: ago(1)},
			"thinking · model call 5s · last output 1s ago"},
	}
	for _, c := range cases {
		if got := describeActivity(c.info, now); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The runner is what feeds the line: each delta kind stamps its phase, and
// the call is marked finished when it returns.
func TestRunnerStampsTheCallPhase(t *testing.T) {
	m := &ExecMetrics{TurnStart: time.Now()}
	fp := &scriptedProvider{stream: &scriptedStream{
		deltas: []provider.StreamDelta{
			{Type: provider.DeltaReasoning, Text: "planning"},
			{Type: provider.DeltaText, Text: "写好了"},
			{Type: provider.DeltaToolCall, ToolName: "write_file"},
		},
		resp: &provider.Response{Content: "写好了"},
	}}
	r := &Runner{provider: fp, metrics: m}
	if _, err := r.callLLM(context.Background(), &provider.Request{}); err != nil {
		t.Fatal(err)
	}
	if m.LastReasoningAt.IsZero() || m.ReplyRunes != 3 || m.ToolCallDraft != "write_file" {
		t.Fatalf("phase not stamped: reasoning=%v runes=%d draft=%q", m.LastReasoningAt, m.ReplyRunes, m.ToolCallDraft)
	}
	if !m.LLMCallStart.IsZero() {
		t.Fatal("the call is still marked in flight after it returned")
	}

	// The next call starts clean.
	m.BeginLLMCall(time.Now())
	if m.ReplyRunes != 0 || m.ToolCallDraft != "" || !m.LastReasoningAt.IsZero() {
		t.Fatal("a new call inherited the previous call's phase")
	}
}

func TestParentProgressWakeCarriesTheActivityLine(t *testing.T) {
	m := NewManager(nil)
	p := &ProgressScanner{mgr: m}
	at := time.Date(2026, 11, 20, 14, 3, 12, 0, time.Local)
	info := msg.ThreadInfo{ElapsedSec: 230, TotalToolCalls: 23, LLMCallStart: at.Add(-time.Second)}
	p.deliverToAncestor("discord:1:threads:x", "discord:1", info, at, describeActivity(info, at), "⏳ note")
	th := m.threads["discord:1"]
	if th == nil {
		t.Fatal("parent not woken")
	}
	w := <-th.inbox
	if !strings.Contains(w.Message, " · as of 14:03:12 · running 3m50s · 23 steps\nactivity: waiting_first_output · model call 1s\n") {
		t.Fatalf("wake body lacks the activity line:\n%s", w.Message)
	}
}
