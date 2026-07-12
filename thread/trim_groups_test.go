package thread

import (
	"fmt"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/provider"
)

// toolGroup builds an assistant+tool_call message paired with its tool result.
// The marker is embedded in the tool result so the test can detect drops.
func toolGroup(id, marker string, padBytes int) []provider.Message {
	asst := provider.AssistantMessageWithTools("", "", nil, []provider.ToolCall{
		{ID: id, Type: "function", Function: provider.FunctionCall{Name: "noop", Arguments: "{}"}},
	})
	tool := provider.Message{Role: "tool", ToolCallID: id, Content: marker + " " + strings.Repeat("x", padBytes)}
	return []provider.Message{asst, tool}
}

// chatTurn builds one complete conversation turn: a user message, nGroups tool
// groups, and a final assistant answer. marker tags every message of the turn.
func chatTurn(marker string, nGroups, padBytes int) []provider.Message {
	msgs := []provider.Message{provider.UserMessage("USER_" + marker)}
	for i := 0; i < nGroups; i++ {
		msgs = append(msgs, toolGroup(fmt.Sprintf("%s_c%d", marker, i), fmt.Sprintf("TOOL_%s_%d", marker, i), padBytes)...)
	}
	return append(msgs, provider.AssistantMessage("ANSWER_"+marker))
}

func joinedContent(msgs []provider.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Role)
		sb.WriteString(" ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// assertNoOrphanTools fails if any tool result appears without the assistant
// message that issued its tool call — the boundary the halving cut must respect.
func assertNoOrphanTools(t *testing.T, msgs []provider.Message) {
	t.Helper()
	seen := map[string]bool{}
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			seen[tc.ID] = true
		}
		if m.Role == "tool" && !seen[m.ToolCallID] {
			t.Errorf("message %d: tool result %q orphaned from its call", i, m.ToolCallID)
		}
	}
}

// TestTrimMessageGroups_HalvesAtTurnBoundary is the core guarantee: over
// budget, the guard drops the oldest half of the conversation, cutting only at
// user-message boundaries — so the head after the system prompt is a user
// message, tool results keep their calls, and the newest turns survive intact.
func TestTrimMessageGroups_HalvesAtTurnBoundary(t *testing.T) {
	msgs := []provider.Message{provider.SystemMessage("SYSTEM_PROMPT")}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, chatTurn(fmt.Sprintf("T%d", i), 2, 2000)...)
	}

	total := EstimateMessagesTokens(msgs)
	budget := total - 1000 // just crossed the line → target = total/2
	out := trimMessageGroups(msgs, 0, budget)
	got := joinedContent(out)

	if !strings.Contains(got, "SYSTEM_PROMPT") {
		t.Error("system prompt must be preserved")
	}
	if out[1].Role != "user" {
		t.Errorf("message after system prompt must be a user message, got %q", out[1].Role)
	}
	if strings.Contains(got, "USER_T0") {
		t.Error("oldest turn should have been dropped")
	}
	if !strings.Contains(got, "USER_T9") || !strings.Contains(got, "ANSWER_T9") {
		t.Error("newest turn must be kept intact")
	}
	assertNoOrphanTools(t, out)

	if kept := EstimateMessagesTokens(out); kept > total/2+total/10 {
		t.Errorf("halving kept %d tokens, want ≈ half of %d", kept, total)
	}
}

// TestTrimMessageGroups_RepeatedHalving: when one halving is not enough, the
// target halves again until it fits under budget (simple and brutal by design).
func TestTrimMessageGroups_RepeatedHalving(t *testing.T) {
	msgs := []provider.Message{provider.SystemMessage("sys")}
	for i := 0; i < 20; i++ {
		msgs = append(msgs, chatTurn(fmt.Sprintf("T%d", i), 1, 2000)...)
	}

	total := EstimateMessagesTokens(msgs)
	budget := total / 4 // needs at least two halvings
	out := trimMessageGroups(msgs, 0, budget)

	if kept := EstimateMessagesTokens(out); kept > budget {
		t.Errorf("result %d still exceeds budget %d after repeated halving", kept, budget)
	}
	if !strings.Contains(joinedContent(out), "USER_T19") {
		t.Error("newest turn must survive repeated halving")
	}
	assertNoOrphanTools(t, out)
}

// TestTrimMessageGroups_HeadStableUntilNextCrossing pins the property the
// WS-pool delta path depends on: one halving moves the head ONCE, and the next
// several turns of growth re-cut at the SAME boundary instead of advancing it
// every call. The pre-halving trim-to-the-line behavior advanced the head per
// call — measured live (2026-07-12, gpt-5.6-terra) as 4 consecutive ~175K
// full-context sends at ~0% cache hit inside a single Tier-2 turn.
func TestTrimMessageGroups_HeadStableUntilNextCrossing(t *testing.T) {
	msgs := []provider.Message{provider.SystemMessage("SYSTEM_PROMPT")}
	for i := 0; i < 40; i++ {
		msgs = append(msgs, chatTurn(fmt.Sprintf("T%d", i), 1, 2000)...)
	}
	budget := EstimateMessagesTokens(msgs) - 1000

	out1 := trimMessageGroups(append([]provider.Message{}, msgs...), 0, budget)
	head := out1[1].Content

	grown := append([]provider.Message{}, msgs...)
	for turn := 0; turn < 5; turn++ {
		grown = append(grown, chatTurn(fmt.Sprintf("NEW%d", turn), 1, 2000)...)
		out := trimMessageGroups(append([]provider.Message{}, grown...), 0, budget)
		if got := out[1].Content; got != head {
			t.Fatalf("turn %d: head advanced (%q → %q) — growth within the halved margin must not move the cut",
				turn, head, got)
		}
	}
}

// TestTrimMessageGroups_MegaTurnFallsBackToGroups: when the final turn ALONE
// exceeds the target (one giant agentic turn — the in-loop guard's case),
// stage 2 drops that turn's oldest tool groups, keeping the most recent group
// and the turn's user message.
func TestTrimMessageGroups_MegaTurnFallsBackToGroups(t *testing.T) {
	msgs := []provider.Message{
		provider.SystemMessage("SYSTEM_PROMPT"),
		provider.UserMessage("TASK_MARKER do the thing"),
	}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, toolGroup(fmt.Sprintf("c%d", i), fmt.Sprintf("GROUP_%d", i), 4000)...)
	}
	msgs = append(msgs, provider.AssistantMessage("FINAL_ANSWER"))

	budget := EstimateMessagesTokens(msgs) / 2
	out := trimMessageGroups(msgs, 0, budget)
	got := joinedContent(out)

	if !strings.Contains(got, "SYSTEM_PROMPT") {
		t.Error("system prompt must be preserved")
	}
	if !strings.Contains(got, "TASK_MARKER") {
		t.Error("the current turn's user message must be preserved")
	}
	if strings.Contains(got, "GROUP_0") {
		t.Error("oldest tool group should have been dropped")
	}
	if !strings.Contains(got, "GROUP_4") {
		t.Error("most recent tool group must be kept")
	}
	if !strings.Contains(got, "FINAL_ANSWER") {
		t.Error("final assistant message must be kept")
	}
	if EstimateMessagesTokens(out) > budget {
		t.Errorf("trimmed result %d still exceeds budget %d", EstimateMessagesTokens(out), budget)
	}
	assertNoOrphanTools(t, out)
}

// TestTrimMessageGroups_KeepsWhenNothingDroppable: a single turn with a single
// huge group has nothing safe to drop — the guard returns the messages
// unchanged (and logs, rather than silently corrupting the turn).
func TestTrimMessageGroups_KeepsWhenNothingDroppable(t *testing.T) {
	msgs := []provider.Message{
		provider.SystemMessage("sys"),
		provider.UserMessage("TASK"),
	}
	msgs = append(msgs, toolGroup("c0", "ONLY_GROUP", 8000)...) // single huge group, over budget

	out := trimMessageGroups(msgs, 0, 100) // budget far below total
	if len(out) != len(msgs) {
		t.Errorf("with nothing droppable, expected unchanged length %d, got %d", len(msgs), len(out))
	}
	if !strings.Contains(joinedContent(out), "TASK") {
		t.Error("current turn's user message must never be dropped")
	}
}
