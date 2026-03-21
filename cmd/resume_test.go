package cmd

import (
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
)

func TestIsIncompleteSession(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		messages []provider.Message
		want     bool
	}{
		{
			name:     "empty session",
			messages: nil,
			want:     false,
		},
		{
			name: "ends with assistant no tool_calls — complete",
			messages: []provider.Message{
				{Role: "user", Content: "hello", Timestamp: now},
				{Role: "assistant", Content: "hi there", Timestamp: now},
			},
			want: false,
		},
		{
			name: "ends with user message — incomplete",
			messages: []provider.Message{
				{Role: "user", Content: "hello", Timestamp: now},
			},
			want: true,
		},
		{
			name: "ends with tool result — incomplete",
			messages: []provider.Message{
				{Role: "user", Content: "read this", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Content: "file contents", Timestamp: now},
			},
			want: true,
		},
		{
			name: "ends with user after sanitize drops empty assistant — incomplete",
			// Input is already sanitized (as from ReadFile): assistant with
			// unanswered tool_calls gets stripped to empty and dropped.
			// After sanitize: [user] — incomplete.
			messages: []provider.Message{
				{Role: "user", Content: "do something", Timestamp: now},
			},
			want: true,
		},
		{
			name: "partial tool results after sanitize — ends with tool, incomplete",
			// After sanitize: assistant keeps only tc1 (answered), tc2 stripped.
			// Sequence: [user, assistant(tc1), tool(tc1)] — ends with tool.
			messages: []provider.Message{
				{Role: "user", Content: "do two things", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
					{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}},
				}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Content: "result1", Timestamp: now},
			},
			want: true,
		},
		{
			name: "complete after tool round — assistant final response",
			messages: []provider.Message{
				{Role: "user", Content: "read this", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Content: "file contents", Timestamp: now},
				{Role: "assistant", Content: "Here is the file", Timestamp: now},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isIncompleteSession(tt.messages)
			if got != tt.want {
				t.Errorf("isIncompleteSession() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindLastUserMessage(t *testing.T) {
	now := time.Now()

	t.Run("finds last user message", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "first", Timestamp: now},
			{Role: "assistant", Content: "reply", Timestamp: now},
			{Role: "user", Content: "second", Timestamp: now},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1"}}, Timestamp: now},
			{Role: "tool", ToolCallID: "tc1", Content: "result", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok || msg.Content != "second" {
			t.Fatalf("expected 'second', got %q ok=%v", msg.Content, ok)
		}
	})

	t.Run("no user messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "assistant", Content: "solo", Timestamp: now},
		}
		_, ok := findLastUserMessage(msgs)
		if ok {
			t.Fatal("expected ok=false")
		}
	})
}

func TestIsResumableSessionKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"telegram:123", true},
		{"discord:456", true},
		{"feishu:789", true},
		{"cli", false},
		{"cron:tidyup", false},
		{"telegram:123:threads:abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := isResumableSessionKey(tt.key); got != tt.want {
				t.Errorf("isResumableSessionKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
