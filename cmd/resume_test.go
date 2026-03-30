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
// frontmatter, matching what markInjected produces for mid-execution messages.
func injectedMsg(source, body string) string {
	return "---\nsource: " + source + "\ninjected: true\n---\n\n" + body
}

// normalMsg builds a user message Content with normal YAML frontmatter.
func normalMsg(source, body string) string {
	return "---\nsource: " + source + "\n---\n\n" + body
}

func TestFindLastUserMessage(t *testing.T) {
	now := time.Now()

	t.Run("finds last resumable message", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "first"), Source: "telegram", Timestamp: now},
			{Role: "assistant", Content: "reply", Timestamp: now},
			{Role: "user", Content: normalMsg("telegram", "second"), Source: "telegram", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		_, body, _ := thread.SplitFrontmatter(msg.Content)
		if body != "\nsecond" {
			t.Fatalf("expected body 'second', got %q", body)
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

	t.Run("skips mid-execution injected messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "original request"), Source: "telegram", Timestamp: now},
			{Role: "assistant", Content: "working...", Timestamp: now},
			{Role: "user", Content: injectedMsg("telegram", "follow-up"), Source: "telegram", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		_, body, _ := thread.SplitFrontmatter(msg.Content)
		if body != "\noriginal request" {
			t.Fatalf("expected 'original request', got %q", body)
		}
	})

	t.Run("resumable sources found", func(t *testing.T) {
		for _, source := range []string{"telegram", "discord", "feishu", "cli", "web", "wecom", "socket", "user_active", "cron", "child_task", "child_completed", "cron_finished", "external"} {
			msgs := []provider.Message{
				{Role: "user", Content: normalMsg(source, "msg"), Source: source, Timestamp: now},
			}
			_, ok := findLastUserMessage(msgs)
			if !ok {
				t.Errorf("expected source %q to be resumable", source)
			}
		}
	})

	t.Run("non-resumable sources skipped", func(t *testing.T) {
		for _, source := range []string{"heartbeat", "compression", "resume", "sleep_completed"} {
			msgs := []provider.Message{
				{Role: "user", Content: normalMsg(source, "msg"), Source: source, Timestamp: now},
			}
			_, ok := findLastUserMessage(msgs)
			if ok {
				t.Errorf("expected source %q to be non-resumable", source)
			}
		}
	})

	t.Run("skips non-resumable to find resumable", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "original"), Source: "telegram", Timestamp: now},
			{Role: "user", Content: normalMsg("heartbeat", "pulse"), Source: "heartbeat", Timestamp: now},
			{Role: "user", Content: normalMsg("compression", "compact"), Source: "compression", Timestamp: now},
		}
		msg, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		_, body, _ := thread.SplitFrontmatter(msg.Content)
		if body != "\noriginal" {
			t.Fatalf("expected 'original', got %q", body)
		}
	})

	t.Run("all non-resumable — returns false", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("heartbeat", "h"), Source: "heartbeat", Timestamp: now},
			{Role: "user", Content: normalMsg("compression", "c"), Source: "compression", Timestamp: now},
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
