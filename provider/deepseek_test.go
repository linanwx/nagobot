package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToDSMessagesReasoningRules(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "query"},
		// Tool-call assistant #1: MUST carry reasoning_content
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "turn1 thinking",
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "search", Arguments: `{"q":"x"}`},
			}},
		},
		{Role: "tool", Content: "result1", ToolCallID: "c1", Name: "search"},
		// Tool-call assistant #2: MUST carry reasoning_content
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "turn2 thinking",
			ToolCalls: []ToolCall{{
				ID: "c2", Type: "function",
				Function: FunctionCall{Name: "search", Arguments: `{"q":"y"}`},
			}},
		},
		{Role: "tool", Content: "result2", ToolCallID: "c2", Name: "search"},
		// Older non-tool-call assistant (ignored by API): SHOULD be omitted
		{
			Role:             "assistant",
			Content:          "intermediate answer",
			ReasoningContent: "turn3 thinking",
		},
		{Role: "user", Content: "next question"},
		// Last assistant (final answer): may carry reasoning_content
		{
			Role:             "assistant",
			Content:          "final answer",
			ReasoningContent: "turn4 thinking",
		},
	}

	out := toDSMessages(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("expected %d, got %d", len(msgs), len(out))
	}

	// [1] tool-call round must preserve reasoning_content
	if out[1].ReasoningContent == nil || *out[1].ReasoningContent != "turn1 thinking" {
		t.Errorf("tool-call assistant[1] lost reasoning: %v", out[1].ReasoningContent)
	}
	// [3] second tool-call round must preserve reasoning_content
	if out[3].ReasoningContent == nil || *out[3].ReasoningContent != "turn2 thinking" {
		t.Errorf("tool-call assistant[3] lost reasoning: %v", out[3].ReasoningContent)
	}
	// [5] historical non-tool-call assistant: must be omitted (nil pointer)
	if out[5].ReasoningContent != nil {
		t.Errorf("historical non-tool-call assistant[5] must be omitted, got %q", *out[5].ReasoningContent)
	}
	// [7] last assistant: preserved
	if out[7].ReasoningContent == nil || *out[7].ReasoningContent != "turn4 thinking" {
		t.Errorf("last assistant[7] lost reasoning: %v", out[7].ReasoningContent)
	}

	// Verify wire-format JSON serialization
	body, _ := json.Marshal(out)
	s := string(body)
	if !strings.Contains(s, `"reasoning_content":"turn1 thinking"`) {
		t.Errorf("turn1 reasoning missing on wire: %s", s)
	}
	if !strings.Contains(s, `"reasoning_content":"turn2 thinking"`) {
		t.Errorf("turn2 reasoning missing on wire: %s", s)
	}
	// [5] must be omitted — no "turn3 thinking" anywhere
	if strings.Contains(s, "turn3 thinking") {
		t.Errorf("historical non-tool-call reasoning leaked: %s", s)
	}
}

func TestToDSMessagesEmptyReasoningNotSent(t *testing.T) {
	msgs := []Message{
		// Tool-call assistant with no reasoning (thinking disabled): omit field
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "f", Arguments: `{}`},
			}},
		},
	}
	out := toDSMessages(msgs)
	if out[0].ReasoningContent != nil {
		t.Errorf("expected nil reasoning_content when source empty, got %q", *out[0].ReasoningContent)
	}
	body, _ := json.Marshal(out[0])
	if strings.Contains(string(body), "reasoning_content") {
		t.Errorf("reasoning_content key must be absent: %s", body)
	}
}
