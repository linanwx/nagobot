package thread

import (
	"strings"
	"testing"

	"github.com/linanwx/nagobot/provider"
)

func makeWakeContent(source, thread, session, delivery, action, msg string) string {
	var sb strings.Builder
	sb.WriteString("<wake>\n")
	sb.WriteString("  <source>" + source + "</source>\n")
	sb.WriteString("  <thread>" + thread + "</thread>\n")
	sb.WriteString("  <session>" + session + "</session>\n")
	sb.WriteString("  <time>2026-03-06T10:00:00+08:00 (Thursday, Asia/Shanghai, UTC+08:00)</time>\n")
	sb.WriteString("  <delivery>" + delivery + "</delivery>\n")
	sb.WriteString("  <action>" + action + "</action>\n")
	sb.WriteString("  <message visibility=\"user-visible\">\n" + msg + "\n  </message>\n")
	sb.WriteString("</wake>")
	return sb.String()
}

func TestTrimWakeFields(t *testing.T) {
	// Build 5 wake messages + assistant replies.
	var messages []provider.Message
	for i := 0; i < 5; i++ {
		messages = append(messages,
			provider.Message{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "A user sent a message.", "hello")},
			provider.Message{Role: "assistant", Content: "hi"},
		)
	}

	modified, result := trimWakeFields(messages, 3)
	if !modified {
		t.Fatal("expected modified=true")
	}

	// Count wake messages that still have <thread>.
	trimmed, kept := 0, 0
	for _, m := range result {
		if m.Role != "user" {
			continue
		}
		if !strings.Contains(m.Content, "<wake>") {
			continue
		}
		if strings.Contains(m.Content, "<thread>") {
			kept++
		} else {
			trimmed++
		}
	}
	if kept != 3 {
		t.Errorf("expected 3 kept wake messages, got %d", kept)
	}
	if trimmed != 2 {
		t.Errorf("expected 2 trimmed wake messages, got %d", trimmed)
	}

	// Verify trimmed messages still have <source>, <time>, <message>.
	for _, m := range result {
		if m.Role != "user" || strings.Contains(m.Content, "<thread>") {
			continue
		}
		if !strings.Contains(m.Content, "<source>") {
			t.Error("trimmed message lost <source>")
		}
		if !strings.Contains(m.Content, "<time>") {
			t.Error("trimmed message lost <time>")
		}
		if !strings.Contains(m.Content, "<message") {
			t.Error("trimmed message lost <message>")
		}
		if strings.Contains(m.Content, "<session>") {
			t.Error("trimmed message still has <session>")
		}
		if strings.Contains(m.Content, "<delivery>") {
			t.Error("trimmed message still has <delivery>")
		}
		if strings.Contains(m.Content, "<action>") {
			t.Error("trimmed message still has <action>")
		}
	}
}

func TestTrimWakeFields_UserContentNotStripped(t *testing.T) {
	// User message contains <action> in the message body — must NOT be stripped.
	content := makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "A user sent a message.",
		"I noticed the <action>hint</action> in the wake format")

	messages := []provider.Message{
		{Role: "user", Content: content},
		{Role: "assistant", Content: "yes"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "A user sent a message.", "msg2")},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "A user sent a message.", "msg3")},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "A user sent a message.", "msg4")},
		{Role: "assistant", Content: "ok"},
	}

	_, result := trimWakeFields(messages, 3)

	// The first message should be trimmed (oldest), but the <action> in user content must survive.
	first := result[0].Content
	if !strings.Contains(first, "<action>hint</action>") {
		t.Error("user content <action> was incorrectly stripped")
	}
	// Header <action> should be removed.
	if strings.Contains(first, "A user sent a message.") {
		t.Error("header <action> was not stripped")
	}
}

func TestTrimWakeFields_Idempotent(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello")},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello2")},
		{Role: "assistant", Content: "hi2"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello3")},
		{Role: "assistant", Content: "hi3"},
		{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "hint", "hello4")},
		{Role: "assistant", Content: "hi4"},
	}

	_, result := trimWakeFields(messages, 3)
	modified2, result2 := trimWakeFields(result, 3)
	if modified2 {
		t.Error("second pass should not modify anything (not idempotent)")
	}
	if len(result) != len(result2) {
		t.Error("message count changed on second pass")
	}
}
