package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// Empirical test against DeepSeek V4 (see /tmp/deepseek-empirical) showed that
// the server 400s when the reasoning_content KEY is absent from an assistant
// message's JSON, but accepts "reasoning_content": "" (empty string) on any
// assistant including tool_call rounds. This test locks the invariant so a
// future refactor cannot re-introduce the v1.4.56 regression.
func TestToDSMessagesAlwaysIncludesReasoningKey(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "q"},
		// Tool-call assistant with no stored reasoning (e.g. trimmed) — wire must
		// still carry `reasoning_content: ""`, else DeepSeek 400s.
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "",
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "f", Arguments: `{}`},
			}},
		},
		{Role: "tool", Content: "r", ToolCallID: "c1", Name: "f"},
		// Historical non-tool-call assistant with no reasoning — same requirement.
		{Role: "assistant", Content: "ok", ReasoningContent: ""},
		{Role: "user", Content: "q2"},
		// Final assistant with real reasoning.
		{Role: "assistant", Content: "final", ReasoningContent: "final thought"},
	}

	out := toDSMessages(msgs)
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(body)

	// Each assistant message on the wire must carry the reasoning_content key.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, m := range raw {
		var role string
		if err := json.Unmarshal(m["role"], &role); err != nil {
			t.Fatalf("index %d: role parse: %v", i, err)
		}
		if role != "assistant" {
			continue
		}
		if _, ok := m["reasoning_content"]; !ok {
			t.Errorf("assistant at index %d missing reasoning_content key; DeepSeek will 400. wire=%s", i, wire)
		}
	}

	// Last assistant with real reasoning — value must be the actual text.
	if !strings.Contains(wire, `"reasoning_content":"final thought"`) {
		t.Errorf("last assistant's real reasoning must be on wire: %s", wire)
	}
}

// Mid-chain tool-call assistants must pass their stored reasoning through to
// the wire — sending empty string on fresh (non-trimmed) messages would drop
// the model's prior thinking and degrade multi-step tool-use coherence.
func TestToDSMessagesPassesReasoningForMidChainToolCalls(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "q"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "iter1 thinking",
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "f", Arguments: `{}`},
			}},
		},
		{Role: "tool", Content: "result", ToolCallID: "c1", Name: "f"},
		{Role: "assistant", Content: "done", ReasoningContent: "iter2 thinking"},
	}

	out := toDSMessages(msgs)
	body, _ := json.Marshal(out)
	wire := string(body)

	if !strings.Contains(wire, `"reasoning_content":"iter1 thinking"`) {
		t.Errorf("mid-chain tool_call reasoning must be on wire, got: %s", wire)
	}
	if !strings.Contains(wire, `"reasoning_content":"iter2 thinking"`) {
		t.Errorf("final assistant reasoning must be on wire, got: %s", wire)
	}
}

// Trimmed messages (ReasoningContent cleared to "") must still include the
// reasoning_content key on the wire — just with empty value.
func TestToDSMessagesTrimmedReasoningSendsEmptyString(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "q"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "", // trimmed
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "f", Arguments: `{}`},
			}},
		},
	}
	out := toDSMessages(msgs)
	if out[1].ReasoningContent == nil {
		t.Fatal("reasoning_content pointer must be non-nil (key must appear on wire)")
	}
	if *out[1].ReasoningContent != "" {
		t.Errorf("expected empty string, got %q", *out[1].ReasoningContent)
	}
	body, _ := json.Marshal(out[1])
	if !strings.Contains(string(body), `"reasoning_content":""`) {
		t.Errorf("expected explicit empty string in wire: %s", body)
	}
}
