package provider

import (
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
