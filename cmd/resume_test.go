package cmd

import (
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread"
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

// injectedMsg builds a user message Content with `injected: true` in YAML
// frontmatter, matching what buildWakePayload produces for injected sources.
func injectedMsg(source, body string) string {
	return "---\nsource: " + source + "\ninjected: true\n---\n\n" + body
}

// normalMsg builds a user message Content with normal YAML frontmatter
// (no injected field), matching what buildWakePayload produces for
// reasoning-initiating sources.
func normalMsg(source, body string) string {
	return "---\nsource: " + source + "\n---\n\n" + body
}

func TestFindLastUserMessage(t *testing.T) {
	now := time.Now()

	t.Run("finds last user message", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "first"), Timestamp: now},
			{Role: "assistant", Content: "reply", Timestamp: now},
			{Role: "user", Content: normalMsg("telegram", "second"), Timestamp: now},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1"}}, Timestamp: now},
			{Role: "tool", ToolCallID: "tc1", Content: "result", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if _, body, fmOk := thread.SplitFrontmatter(msg.Content); fmOk {
			if body != "\nsecond" {
				t.Fatalf("expected body 'second', got %q", body)
			}
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

	t.Run("skips injected resume messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "original request"), Timestamp: now},
			{Role: "assistant", Content: "partial", Timestamp: now},
			{Role: "user", Content: injectedMsg("resume", "resume msg 1"), Timestamp: now},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "sleep_thread"}}}, Timestamp: now},
			{Role: "tool", ToolCallID: "tc1", Name: "sleep_thread", Content: "ok", Timestamp: now},
			{Role: "user", Content: injectedMsg("resume", "resume msg 2"), Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if _, body, fmOk := thread.SplitFrontmatter(msg.Content); !fmOk || body != "\noriginal request" {
			t.Fatalf("expected 'original request', got %q", body)
		}
	})

	t.Run("skips injected heartbeat messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("discord", "user question"), Timestamp: now},
			{Role: "assistant", Content: "thinking...", Timestamp: now},
			{Role: "user", Content: injectedMsg("heartbeat", "heartbeat pulse"), Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if _, body, fmOk := thread.SplitFrontmatter(msg.Content); !fmOk || body != "\nuser question" {
			t.Fatalf("expected 'user question', got %q", body)
		}
	})

	t.Run("skips injected but keeps non-injected system sources", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "real msg"), Timestamp: now},
			{Role: "user", Content: injectedMsg("compression", "compressed"), Timestamp: now},
			{Role: "user", Content: injectedMsg("heartbeat", "heartbeat"), Timestamp: now},
			{Role: "user", Content: injectedMsg("resume", "resumed"), Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if _, body, fmOk := thread.SplitFrontmatter(msg.Content); !fmOk || body != "\nreal msg" {
			t.Fatalf("expected 'real msg', got %q", body)
		}
	})

	t.Run("finds cron as original request", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("cron", "cron job context"), Timestamp: now},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}}, Timestamp: now},
			{Role: "tool", ToolCallID: "tc1", Content: "file data", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if _, body, fmOk := thread.SplitFrontmatter(msg.Content); !fmOk || body != "\ncron job context" {
			t.Fatalf("expected 'cron job context', got %q", body)
		}
	})

	t.Run("finds child_task as original request", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("child_task", "do subtask"), Timestamp: now},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}}, Timestamp: now},
			{Role: "tool", ToolCallID: "tc1", Content: "result", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if _, body, fmOk := thread.SplitFrontmatter(msg.Content); !fmOk || body != "\ndo subtask" {
			t.Fatalf("expected 'do subtask', got %q", body)
		}
	})

	t.Run("finds sleep_completed as wake source", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "original"), Timestamp: now},
			{Role: "assistant", Content: "scheduled", Timestamp: now},
			{Role: "user", Content: normalMsg("sleep_completed", "timer expired"), Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if _, body, fmOk := thread.SplitFrontmatter(msg.Content); !fmOk || body != "\ntimer expired" {
			t.Fatalf("expected 'timer expired', got %q", body)
		}
	})

	t.Run("all messages are injected — returns false", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: injectedMsg("resume", "resume only"), Timestamp: now},
			{Role: "user", Content: injectedMsg("heartbeat", "heartbeat"), Timestamp: now},
			{Role: "user", Content: injectedMsg("compression", "compressed"), Timestamp: now},
		}
		_, ok := findLastUserMessage(msgs)
		if ok {
			t.Fatal("expected ok=false when all user messages are injected")
		}
	})

	t.Run("plain text without frontmatter — not injected", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "plain text message", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok || msg.Content != "plain text message" {
			t.Fatalf("expected 'plain text message', got %q ok=%v", msg.Content, ok)
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

func TestExtractAgentFromLastMessage(t *testing.T) {
	// Verifies that agent is extracted from the SAME message as findLastUserMessage,
	// not from the first user message (#108).

	t.Run("agent from last user message, not first", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "---\nsource: telegram\nagent: coffee\n---\n\nold msg", Source: "telegram"},
			{Role: "assistant", Content: "reply"},
			{Role: "user", Content: "---\nsource: telegram\nagent: soul\n---\n\nnew msg", Source: "telegram"},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		yamlBlock, _, fmOk := thread.SplitFrontmatter(msg.Content)
		if !fmOk {
			t.Fatal("expected frontmatter")
		}
		got := thread.ExtractFrontmatterValue(yamlBlock, "agent")
		if got != "soul" {
			t.Errorf("expected 'soul' (last msg), got %q", got)
		}
	})

	t.Run("no frontmatter — empty agent", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "plain text message", Source: "telegram"},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		_, _, fmOk := thread.SplitFrontmatter(msg.Content)
		if fmOk {
			t.Error("expected no frontmatter")
		}
	})
}
