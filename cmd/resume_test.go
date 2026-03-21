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
			messages: []provider.Message{
				{Role: "user", Content: "do something", Timestamp: now},
			},
			want: true,
		},
		{
			name: "partial tool results after sanitize — ends with tool, incomplete",
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
		{
			name: "ends with sleep_thread — deliberate completion, not interrupted",
			messages: []provider.Message{
				{Role: "user", Content: "hello", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "sleep_thread"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Name: "sleep_thread", Content: "ok", Timestamp: now},
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

	t.Run("skips resume messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "original request", Source: "telegram", Timestamp: now},
			{Role: "assistant", Content: "partial", Timestamp: now},
			{Role: "user", Content: "resume msg 1", Source: "resume", Timestamp: now},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "sleep_thread"}}}, Timestamp: now},
			{Role: "tool", ToolCallID: "tc1", Name: "sleep_thread", Content: "ok", Timestamp: now},
			{Role: "user", Content: "resume msg 2", Source: "resume", Timestamp: now},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc2", Type: "function", Function: provider.FunctionCall{Name: "sleep_thread"}}}, Timestamp: now},
			{Role: "tool", ToolCallID: "tc2", Name: "sleep_thread", Content: "ok", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok || msg.Content != "original request" {
			t.Fatalf("expected 'original request', got %q ok=%v", msg.Content, ok)
		}
	})

	t.Run("all messages are resume — returns false", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "resume only", Source: "resume", Timestamp: now},
		}
		_, ok := findLastUserMessage(msgs)
		if ok {
			t.Fatal("expected ok=false when all user messages are resume")
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
		{"cron:tidyup", true},
		{"cron:daily-news", true},
		{"telegram:123:threads:2026-03-21-abc", true},
		{"discord:456:threads:2026-03-21-def", true},
		{"cron:tidyup:threads:2026-03-21-ghi", true},
		{"cli", false},
		{"cli:threads:2026-03-21-abc", false},
		{"web:main", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := isResumableSessionKey(tt.key); got != tt.want {
				t.Errorf("isResumableSessionKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestExtractAgentFromSession(t *testing.T) {
	t.Run("extracts agent from YAML frontmatter", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "---\nsource: child_task\nagent: imagereader\n---\n\nAnalyze this image", Source: "child_task"},
		}
		got := extractAgentFromSession(msgs)
		if got != "imagereader" {
			t.Errorf("expected 'imagereader', got %q", got)
		}
	})

	t.Run("skips resume messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "---\nsource: telegram\nagent: soul\n---\n\nhello", Source: "telegram"},
			{Role: "user", Content: "---\nsource: resume\nagent: soul\n---\n\nresume content", Source: "resume"},
		}
		got := extractAgentFromSession(msgs)
		if got != "soul" {
			t.Errorf("expected 'soul', got %q", got)
		}
	})

	t.Run("no agent field — returns empty", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "---\nsource: telegram\n---\n\nhello", Source: "telegram"},
		}
		got := extractAgentFromSession(msgs)
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("no YAML frontmatter — returns empty", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "plain text message", Source: "telegram"},
		}
		got := extractAgentFromSession(msgs)
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}
