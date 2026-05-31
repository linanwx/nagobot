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

// TestTrimMessageGroups_PreservesTaskAndSystem is the core guarantee of the
// unified trim: over budget, it drops the OLDEST assistant+tool_call groups but
// never the system prompt or any user message — including the first task.
func TestTrimMessageGroups_PreservesTaskAndSystem(t *testing.T) {
	msgs := []provider.Message{
		provider.SystemMessage("SYSTEM_PROMPT"),
		provider.UserMessage("TASK_MARKER do the thing"),
	}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, toolGroup(fmt.Sprintf("c%d", i), fmt.Sprintf("GROUP_%d", i), 4000)...)
	}
	msgs = append(msgs, provider.AssistantMessage("FINAL_ANSWER"))

	budget := EstimateMessagesTokens(msgs) / 2 // force dropping the oldest groups
	out := trimMessageGroups(msgs, 0, budget)
	got := joinedContent(out)

	if !strings.Contains(got, "SYSTEM_PROMPT") {
		t.Error("system prompt must be preserved")
	}
	if !strings.Contains(got, "TASK_MARKER") {
		t.Error("first task user message must be preserved")
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
}

// TestTrimMessageGroups_KeepsWhenOneGroup covers the accepted "drop tool groups,
// and if that isn't enough, leave it" behavior: with at most one droppable
// group it returns unchanged rather than dropping user/system content.
func TestTrimMessageGroups_KeepsWhenOneGroup(t *testing.T) {
	msgs := []provider.Message{
		provider.SystemMessage("sys"),
		provider.UserMessage("TASK"),
	}
	msgs = append(msgs, toolGroup("c0", "ONLY_GROUP", 8000)...) // single huge group, over budget

	out := trimMessageGroups(msgs, 0, 100) // budget far below total
	if len(out) != len(msgs) {
		t.Errorf("with one group, expected unchanged length %d, got %d", len(msgs), len(out))
	}
	if !strings.Contains(joinedContent(out), "TASK") {
		t.Error("task message must never be dropped, even when over budget")
	}
}
