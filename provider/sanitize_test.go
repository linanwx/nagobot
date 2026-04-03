package provider

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSanitizeMessages_DropsEmptyAssistant(t *testing.T) {
	messages := []Message{
		UserMessage("hello"),
		{Role: "assistant", Content: "", ReasoningContent: "", ToolCalls: nil}, // empty — should be removed
		AssistantMessage("world"),
	}
	got := SanitizeMessages(messages)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != "user" || got[1].Content != "world" {
		t.Fatalf("unexpected messages: %+v", got)
	}
}

func TestSanitizeMessages_KeepsAssistantWithToolCalls(t *testing.T) {
	messages := []Message{
		UserMessage("hello"),
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "tc1", Type: "function", Function: FunctionCall{Name: "foo", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "tc1", Name: "foo", Content: "bar"},
	}
	got := SanitizeMessages(messages)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
}

func TestSanitizeMessages_DropsOrphanedTool(t *testing.T) {
	messages := []Message{
		UserMessage("hello"),
		{Role: "tool", ToolCallID: "orphan", Name: "foo", Content: "bar"},
		AssistantMessage("world"),
	}
	got := SanitizeMessages(messages)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (orphan tool removed), got %d", len(got))
	}
}

func TestSanitizeMessages_StripsUnansweredToolCalls(t *testing.T) {
	messages := []Message{
		UserMessage("hello"),
		// assistant with 2 tool_calls, but only one has a tool response
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Type: "function", Function: FunctionCall{Name: "f1", Arguments: "{}"}},
			{ID: "tc2", Type: "function", Function: FunctionCall{Name: "f2", Arguments: "{}"}},
		}},
		{Role: "tool", ToolCallID: "tc1", Name: "f1", Content: "ok"},
		// tc2 has no tool response
		AssistantMessage("done"),
	}
	got := SanitizeMessages(messages)
	// Should keep: user, assistant(with only tc1), tool(tc1), assistant
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "tc1" {
		t.Fatalf("expected assistant to keep only tc1, got %v", got[1].ToolCalls)
	}
}

func TestSanitizeMessages_DropsAssistantWithAllUnansweredToolCalls(t *testing.T) {
	messages := []Message{
		UserMessage("hello"),
		// assistant with tool_calls but NO tool responses at all
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Type: "function", Function: FunctionCall{Name: "f1", Arguments: "{}"}},
		}},
		AssistantMessage("fallback"),
	}
	got := SanitizeMessages(messages)
	// assistant loses all tool_calls → becomes empty → dropped
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (empty assistant dropped), got %d", len(got))
	}
}

func TestGetContent_ReturnsCompressedWhenSet(t *testing.T) {
	m := Message{Role: "user", Content: "original long content", Compressed: "short summary"}
	if got := m.GetContent(); got != "short summary" {
		t.Fatalf("GetContent() = %q, want %q", got, "short summary")
	}
}

func TestGetContent_ReturnsContentWhenNoCompressed(t *testing.T) {
	m := Message{Role: "user", Content: "original content"}
	if got := m.GetContent(); got != "original content" {
		t.Fatalf("GetContent() = %q, want %q", got, "original content")
	}
}

func TestGetContent_ReturnsEmptyWhenBothEmpty(t *testing.T) {
	m := Message{Role: "user"}
	if got := m.GetContent(); got != "" {
		t.Fatalf("GetContent() = %q, want empty", got)
	}
}

func TestSanitizeMessages_KeepsAssistantWithCompressed(t *testing.T) {
	messages := []Message{
		UserMessage("hello"),
		{Role: "assistant", Content: "", Compressed: "compressed reply"},
		AssistantMessage("world"),
	}
	got := SanitizeMessages(messages)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages (assistant with Compressed kept), got %d", len(got))
	}
	if got[1].Compressed != "compressed reply" {
		t.Fatalf("Messages[1].Compressed = %q, want %q", got[1].Compressed, "compressed reply")
	}
}

func TestStripReasoningKeepSignatures_GeminiFormat(t *testing.T) {
	// Gemini-style: text+sig, functionCall+sig, thought part.
	details := json.RawMessage(`[
		{"text":"hello","thoughtSignature":"sig1"},
		{"functionCall":{"name":"sleep_thread","args":{}},"thoughtSignature":"sig2"},
		{"text":"thinking...","thought":true}
	]`)
	got := StripReasoningKeepSignatures(details)
	if got == nil {
		t.Fatal("expected non-nil result for Gemini format with functionCall+sig")
	}
	// Should keep only the functionCall+sig part.
	var parts []json.RawMessage
	if err := json.Unmarshal(got, &parts); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 kept part, got %d", len(parts))
	}
	var p struct {
		FunctionCall     *struct{ Name string } `json:"functionCall"`
		ThoughtSignature string                 `json:"thoughtSignature"`
	}
	if err := json.Unmarshal(parts[0], &p); err != nil {
		t.Fatalf("unmarshal part: %v", err)
	}
	if p.FunctionCall == nil || p.FunctionCall.Name != "sleep_thread" {
		t.Fatalf("expected functionCall sleep_thread, got %+v", p.FunctionCall)
	}
	if p.ThoughtSignature != "sig2" {
		t.Fatalf("expected sig2, got %s", p.ThoughtSignature)
	}
}

func TestStripReasoningKeepSignatures_NonGeminiFormat(t *testing.T) {
	// Anthropic-style: no functionCall fields.
	details := json.RawMessage(`[{"type":"thinking","thinking":"deep thought","signature":"abc"}]`)
	got := StripReasoningKeepSignatures(details)
	if got != nil {
		t.Fatalf("expected nil for non-Gemini format, got %s", got)
	}
}

func TestStripReasoningKeepSignatures_Empty(t *testing.T) {
	if got := StripReasoningKeepSignatures(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %s", got)
	}
	if got := StripReasoningKeepSignatures(json.RawMessage(`[]`)); got != nil {
		t.Fatalf("expected nil for empty array, got %s", got)
	}
}

func TestSanitizeMessages_PreservesIDAndTimestamp(t *testing.T) {
	ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	messages := []Message{
		{Role: "user", Content: "hello", ID: "sess:1709000000000:000", Timestamp: ts},
		{Role: "assistant", Content: "hi", ID: "sess:1709000000000:001", Timestamp: ts,
			ToolCalls: []ToolCall{{ID: "tc1", Type: "function", Function: FunctionCall{Name: "f", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "tc1", Name: "f", Content: "ok", ID: "sess:1709000000000:002", Timestamp: ts},
	}
	got := SanitizeMessages(messages)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	for i, m := range got {
		if m.ID == "" {
			t.Fatalf("Messages[%d].ID should be preserved, got empty", i)
		}
		if m.Timestamp.IsZero() {
			t.Fatalf("Messages[%d].Timestamp should be preserved, got zero", i)
		}
	}
}
