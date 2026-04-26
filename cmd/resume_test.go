package cmd

import (
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread"
)

func TestIsUserTurnComplete(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		messages   []provider.Message
		userMsgIdx int
		want       bool
	}{
		{
			name: "user message is last — no response, incomplete",
			messages: []provider.Message{
				{Role: "user", Content: "hello", Timestamp: now},
			},
			userMsgIdx: 0,
			want:       false,
		},
		{
			name: "assistant replied without tool_calls — complete",
			messages: []provider.Message{
				{Role: "user", Content: "hello", Timestamp: now},
				{Role: "assistant", Content: "hi there", Timestamp: now},
			},
			userMsgIdx: 0,
			want:       true,
		},
		{
			name: "tool round then final response — complete",
			messages: []provider.Message{
				{Role: "user", Content: "read this", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Content: "file contents", Timestamp: now},
				{Role: "assistant", Content: "Here is the file", Timestamp: now},
			},
			userMsgIdx: 0,
			want:       true,
		},
		{
			name: "tool round interrupted — ends with tool result, incomplete",
			messages: []provider.Message{
				{Role: "user", Content: "read this", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Content: "file contents", Timestamp: now},
			},
			userMsgIdx: 0,
			want:       false,
		},
		{
			name: "ends with dispatch({}) — deliberate silent completion",
			messages: []provider.Message{
				{Role: "user", Content: "hello", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "dispatch"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Name: "dispatch", Content: `{"executed":[],"outcome":"turn-terminated-silent"}`, Timestamp: now},
			},
			userMsgIdx: 0,
			want:       true,
		},
		{
			name: "user turn complete but heartbeat interrupted after — scoped to user turn only",
			messages: []provider.Message{
				{Role: "user", Content: normalMsg("telegram", "hello"), Source: "telegram", Timestamp: now},
				{Role: "assistant", Content: "hi there", Timestamp: now},
				// heartbeat starts a new turn
				{Role: "user", Content: normalMsg("heartbeat", "pulse"), Source: "heartbeat", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "some_tool"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Content: "result", Timestamp: now},
				// heartbeat interrupted here — but user turn at idx 0 is complete
			},
			userMsgIdx: 0,
			want:       true,
		},
		{
			name: "injected message does not end the turn scope",
			messages: []provider.Message{
				{Role: "user", Content: normalMsg("telegram", "do stuff"), Source: "telegram", Timestamp: now},
				{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read_file"}}}, Timestamp: now},
				{Role: "tool", ToolCallID: "tc1", Content: "file contents", Timestamp: now},
				{Role: "user", Content: injectedMsg("telegram", "follow-up"), Source: "telegram", Timestamp: now},
				{Role: "assistant", Content: "done", Timestamp: now},
			},
			userMsgIdx: 0,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUserTurnComplete(tt.messages, tt.userMsgIdx)
			if got != tt.want {
				t.Errorf("isUserTurnComplete() = %v, want %v", got, tt.want)
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
		msg, idx, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if idx != 2 {
			t.Fatalf("expected idx 2, got %d", idx)
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
		_, _, ok := findLastUserMessage(msgs)
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
		msg, idx, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if idx != 0 {
			t.Fatalf("expected idx 0, got %d", idx)
		}
		_, body, _ := thread.SplitFrontmatter(msg.Content)
		if body != "\noriginal request" {
			t.Fatalf("expected 'original request', got %q", body)
		}
	})

	t.Run("non-resumable sources skipped", func(t *testing.T) {
		// "resume" is intentionally NOT excluded — see resume.go nonResumableSources
		// docstring (commit 6f0dd0b): excluding it would cause infinite resume loops
		// because the original interrupted message would be re-found on every restart.
		for _, source := range []string{"heartbeat", "compression"} {
			msgs := []provider.Message{
				{Role: "user", Content: normalMsg(source, "msg"), Source: source, Timestamp: now},
			}
			_, _, ok := findLastUserMessage(msgs)
			if ok {
				t.Errorf("expected source %q to be non-resumable", source)
			}
		}
	})

	t.Run("other sources are resumable", func(t *testing.T) {
		for _, source := range []string{"telegram", "discord", "cron", "child_task", "child_completed", "cron_finished", "external", "sleep_completed", "resume"} {
			msgs := []provider.Message{
				{Role: "user", Content: normalMsg(source, "msg"), Source: source, Timestamp: now},
			}
			_, _, ok := findLastUserMessage(msgs)
			if !ok {
				t.Errorf("expected source %q to be resumable", source)
			}
		}
	})

	t.Run("skips non-resumable to find resumable", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: normalMsg("telegram", "original"), Source: "telegram", Timestamp: now},
			{Role: "user", Content: normalMsg("heartbeat", "pulse"), Source: "heartbeat", Timestamp: now},
			{Role: "user", Content: normalMsg("compression", "compact"), Source: "compression", Timestamp: now},
		}
		msg, idx, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		if idx != 0 {
			t.Fatalf("expected idx 0, got %d", idx)
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
		_, _, ok := findLastUserMessage(msgs)
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
		msg, _, ok := findLastUserMessage(msgs)
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
		msg, _, ok := findLastUserMessage(msgs)
		if !ok {
			t.Fatal("expected to find user message")
		}
		_, _, fmOk := thread.SplitFrontmatter(msg.Content)
		if fmOk {
			t.Error("expected no frontmatter")
		}
	})
}
