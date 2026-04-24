package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToMMMessagesCarriesReasoning(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "first-turn thinking",
			ToolCalls: []ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: FunctionCall{Name: "web_search", Arguments: `{"q":"x"}`},
			}},
		},
		{Role: "tool", Content: "search result", ToolCallID: "call_1", Name: "web_search"},
		{
			Role:             "assistant",
			Content:          "final answer",
			ReasoningContent: "second-turn thinking",
		},
	}

	out := toMMMessages(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(out))
	}

	// assistant with tool_calls — must carry reasoning_content
	if out[1].ReasoningContent == nil || *out[1].ReasoningContent != "first-turn thinking" {
		t.Errorf("assistant[1] reasoning_content lost; got %v", out[1].ReasoningContent)
	}
	// assistant final answer — must carry reasoning_content
	if out[3].ReasoningContent == nil || *out[3].ReasoningContent != "second-turn thinking" {
		t.Errorf("assistant[3] reasoning_content lost; got %v", out[3].ReasoningContent)
	}

	// JSON must include the reasoning_content field (MiMo API requirement)
	body, err := json.Marshal(out[3])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"reasoning_content":"second-turn thinking"`) {
		t.Errorf("reasoning_content not serialized to wire format: %s", body)
	}
}

func TestToMMMessagesOmitsReasoningWhenEmpty(t *testing.T) {
	// Compression (ApplyCompressedMessage) clears ReasoningContent when
	// ReasoningTrimmed is set, so an empty string must not serialize.
	msgs := []Message{
		{Role: "assistant", Content: "trimmed turn", ReasoningContent: ""},
	}
	out := toMMMessages(msgs)
	if out[0].ReasoningContent != nil {
		t.Errorf("expected nil reasoning_content when empty, got %q", *out[0].ReasoningContent)
	}
	body, _ := json.Marshal(out[0])
	if strings.Contains(string(body), "reasoning_content") {
		t.Errorf("reasoning_content key must be omitted when empty: %s", body)
	}
}
