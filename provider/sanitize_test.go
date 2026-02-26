package provider

import "testing"

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
