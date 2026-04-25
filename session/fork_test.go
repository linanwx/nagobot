package session

import (
	"strings"
	"testing"

	"github.com/linanwx/nagobot/provider"
)

func TestForkMessages_UserFrontmatterStripped(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "---\nsource: telegram\nthread: thread-1234\nsession: telegram:123\ntime: 2026-04-10T10:00:00+08:00\nmodel: openrouter/gemini\ndelivery: telegram\nsender: user\naction: A user sent a message.\n---\n\nHello world"},
		{Role: "assistant", Content: "Hi there!"},
	}

	result := ForkMessages(msgs)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}

	// User message should only have sender + time in frontmatter.
	user := result[0]
	if !strings.Contains(user.Content, "sender: user") {
		t.Error("expected sender to be preserved")
	}
	if !strings.Contains(user.Content, "time: 2026-04-10T10:00:00+08:00") {
		t.Error("expected time to be preserved")
	}
	if strings.Contains(user.Content, "source:") {
		t.Error("source should be stripped from frontmatter")
	}
	if strings.Contains(user.Content, "thread:") {
		t.Error("thread should be stripped from frontmatter")
	}
	if strings.Contains(user.Content, "delivery:") {
		t.Error("delivery should be stripped from frontmatter")
	}
	if strings.Contains(user.Content, "Hello world") {
		// body should be preserved
	} else {
		t.Error("body should be preserved")
	}
}

func TestForkMessages_UserNoFrontmatter(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "Plain message without frontmatter"},
		{Role: "assistant", Content: "OK"},
	}

	result := ForkMessages(msgs)
	if result[0].Content != "Plain message without frontmatter" {
		t.Errorf("plain user message should be unchanged, got %q", result[0].Content)
	}
}

func TestForkMessages_SystemSenderKept(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "---\nsender: system\ntime: 2026-04-10T10:00:00+08:00\nsource: heartbeat\n---\n\nheartbeat wake", Source: "heartbeat"},
		{Role: "assistant", Content: "Processing heartbeat..."},
	}

	result := ForkMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (system sender kept), got %d", len(result))
	}
}

func TestForkMessages_HeartbeatTrimSkipped(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "---\nsender: user\ntime: 2026-04-10T08:00:00+08:00\n---\n\nHello"},
		{Role: "assistant", Content: "Hi!"},
		// Heartbeat turn with HeartbeatTrim — should be skipped entirely.
		{Role: "user", Content: "heartbeat wake", Source: "heartbeat", HeartbeatTrim: false},
		{Role: "assistant", Content: "", HeartbeatTrim: true, ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "dispatch", Arguments: `{"sends":[]}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "dispatch", Content: "ok", HeartbeatTrim: true},
		// Another user turn after.
		{Role: "user", Content: "---\nsender: user\ntime: 2026-04-10T12:00:00+08:00\n---\n\nStill here"},
		{Role: "assistant", Content: "Welcome back!"},
	}

	result := ForkMessages(msgs)

	// Should have: turn1 (user+assistant) + turn3 (user+assistant) = 4 messages.
	// Turn2 (heartbeat trim) should be skipped.
	if len(result) != 4 {
		t.Fatalf("expected 4 messages (heartbeat turn skipped), got %d", len(result))
	}
	if !strings.Contains(result[2].Content, "Still here") {
		t.Error("third turn user message should be present")
	}
}

func TestForkMessages_ToolCallsFolded(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "Read the config file"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{
				Name:      "read_file",
				Arguments: `{"path": "/etc/config.yaml"}`,
			}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "read_file", Content: "key: value\nhost: localhost"},
		{Role: "assistant", Content: "The config has key=value and host=localhost."},
	}

	result := ForkMessages(msgs)

	// user + assistant(tool summary) + assistant(content) = 3 messages.
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}

	// Tool summary message.
	toolMsg := result[1]
	if toolMsg.Role != "assistant" {
		t.Errorf("expected assistant role, got %s", toolMsg.Role)
	}
	if !strings.Contains(toolMsg.Content, "[tool: read_file(") {
		t.Errorf("expected tool summary, got %q", toolMsg.Content)
	}
	if !strings.Contains(toolMsg.Content, "key: value") {
		t.Errorf("expected result preview in summary, got %q", toolMsg.Content)
	}

	// Final assistant message.
	if result[2].Content != "The config has key=value and host=localhost." {
		t.Errorf("assistant content should be preserved verbatim")
	}
}

func TestForkMessages_MultipleToolCalls(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "Build the project"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"cmd":"go build ./..."}`}},
			{ID: "c2", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"cmd":"go test ./..."}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "exec", Content: "ok"},
		{Role: "tool", ToolCallID: "c2", Name: "exec", Content: "PASS"},
		{Role: "assistant", Content: "Build and tests passed."},
	}

	result := ForkMessages(msgs)

	// user + assistant(2 tool summaries) + assistant(content) = 3 messages.
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}

	summary := result[1].Content
	if !strings.Contains(summary, "[tool: exec(") {
		t.Error("expected exec tool summary")
	}
	if strings.Count(summary, "[tool:") != 2 {
		t.Errorf("expected 2 tool summaries, got %d", strings.Count(summary, "[tool:"))
	}
}

func TestForkMessages_ReasoningStripped(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "Think about this"},
		{Role: "assistant", Content: "Here's my answer.", ReasoningContent: "Let me think step by step..."},
	}

	result := ForkMessages(msgs)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[1].ReasoningContent != "" {
		t.Error("reasoning content should be stripped")
	}
	if result[1].Content != "Here's my answer." {
		t.Error("assistant content should be preserved")
	}
}

func TestForkMessages_ToolArgsPreviewTruncated(t *testing.T) {
	// Create args longer than 100 runes.
	longArgs := `{"path": "` + strings.Repeat("a", 200) + `"}`
	msgs := []provider.Message{
		{Role: "user", Content: "Read it"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{
				Name:      "read_file",
				Arguments: longArgs,
			}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "read_file", Content: "short result"},
	}

	result := ForkMessages(msgs)

	summary := result[1].Content
	if !strings.Contains(summary, "...") {
		t.Error("long args should be truncated with ...")
	}
}

func TestForkMessages_ToolResultPreviewTruncated(t *testing.T) {
	// Create result longer than 200 runes.
	longResult := strings.Repeat("行", 300)
	msgs := []provider.Message{
		{Role: "user", Content: "Read it"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: `{}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "read_file", Content: longResult},
	}

	result := ForkMessages(msgs)

	summary := result[1].Content
	// The preview should be truncated to 200 runes + "..."
	if !strings.Contains(summary, "...") {
		t.Error("long result should be truncated with ...")
	}
	// Count CJK runes in preview part (between → and ])
	idx := strings.Index(summary, "→ ")
	if idx < 0 {
		t.Fatal("expected → separator in tool summary")
	}
	preview := summary[idx+len("→ ") : len(summary)-1] // strip trailing ]
	if len([]rune(preview)) > 204 { // 200 + "..."
		t.Errorf("result preview too long: %d runes", len([]rune(preview)))
	}
}

func TestForkMessages_AssistantOnlyToolCalls(t *testing.T) {
	// Assistant with only tool calls (no content) — should produce tool summaries.
	msgs := []provider.Message{
		{Role: "user", Content: "Do it"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"cmd":"ls"}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "exec", Content: "file1\nfile2"},
	}

	result := ForkMessages(msgs)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if !strings.Contains(result[1].Content, "[tool: exec(") {
		t.Errorf("expected tool summary, got %q", result[1].Content)
	}
}

func TestForkMessages_OutputOnlyUserAssistant(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "exec", Content: "ok"},
		{Role: "assistant", Content: "Done"},
	}

	result := ForkMessages(msgs)
	for _, m := range result {
		if m.Role != "user" && m.Role != "assistant" {
			t.Errorf("expected only user/assistant roles, got %s", m.Role)
		}
	}
}

func TestForkMessages_EmptyInput(t *testing.T) {
	result := ForkMessages(nil)
	if len(result) != 0 {
		t.Errorf("expected empty output, got %d messages", len(result))
	}
}

// TestForkMessages_MultiLineActionStripped is a regression test for the
// original Discord wake bug: stripFrontmatter must not leak the body of an
// `action: |-` block scalar into the YAML when reducing to sender+time.
func TestForkMessages_MultiLineActionStripped(t *testing.T) {
	content := "---\n" +
		"source: session\n" +
		"thread: thread-1\n" +
		"session: discord:1\n" +
		"time: 2026-04-22T19:04:57+01:00\n" +
		"sender: system\n" +
		"caller_session_key: discord:1:threads:foo\n" +
		"action: |-\n" +
		"    Another session woke you. caller_session_key = the IMMEDIATE sender.\n" +
		"    End this turn with exactly one of:\n" +
		"    1. dispatch(to=caller) — reply to the waker.\n" +
		"    MUST NOT: use dispatch({}) when you suspect mis-routing.\n" +
		"---\n" +
		"the actual response body"

	msgs := []provider.Message{
		{Role: "user", Content: content, Source: "session"},
	}
	result := ForkMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	got := result[0].Content

	// Action body must NOT appear in the reduced output.
	for _, leak := range []string{
		"Another session woke you",
		"End this turn",
		"MUST NOT: use dispatch",
		"action:",
	} {
		if strings.Contains(got, leak) {
			t.Errorf("action block content %q leaked through stripFrontmatter:\n%s", leak, got)
		}
	}
	// Sender and time should remain.
	if !strings.Contains(got, "sender: system") {
		t.Errorf("sender lost: %s", got)
	}
	if !strings.Contains(got, "time:") {
		t.Errorf("time lost: %s", got)
	}
	// Body preserved.
	if !strings.Contains(got, "the actual response body") {
		t.Errorf("body lost: %s", got)
	}
}

func TestForkMessages_NestedFrontmatterInBodyPreserved(t *testing.T) {
	// User message whose body itself contains another `---\n...---\n` block
	// (e.g. when one wake quotes another). The OUTER frontmatter is reduced,
	// the inner is preserved verbatim.
	content := "---\n" +
		"source: telegram\n" +
		"thread: t1\n" +
		"session: telegram:1\n" +
		"time: 2026-04-10T10:00:00+08:00\n" +
		"sender: user\n" +
		"---\n" +
		"---\ninner: yaml\n---\nquoted body"

	msgs := []provider.Message{{Role: "user", Content: content}}
	result := ForkMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	got := result[0].Content
	if !strings.Contains(got, "---\ninner: yaml\n---\nquoted body") {
		t.Errorf("inner frontmatter must be preserved verbatim in body, got:\n%s", got)
	}
	if !strings.Contains(got, "sender: user") {
		t.Errorf("sender lost: %s", got)
	}
	if strings.Contains(got, "source:") {
		t.Errorf("source should be stripped: %s", got)
	}
}

func TestForkMessages_ToolCallWithContent(t *testing.T) {
	// Assistant message with both tool calls AND content.
	msgs := []provider.Message{
		{Role: "user", Content: "Check and explain"},
		{Role: "assistant", Content: "Let me check that for you.", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"x"}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "read_file", Content: "contents"},
	}

	result := ForkMessages(msgs)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	combined := result[1].Content
	if !strings.Contains(combined, "[tool: read_file(") {
		t.Error("expected tool summary")
	}
	if !strings.Contains(combined, "Let me check that for you.") {
		t.Error("expected assistant content preserved")
	}
}
