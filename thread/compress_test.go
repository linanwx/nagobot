package thread

import (
	"strings"
	"testing"

	"github.com/linanwx/nagobot/provider"
)

func makeWakeContent(source, thread, session, delivery, action, msg string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("source: " + source + "\n")
	sb.WriteString("thread: " + thread + "\n")
	sb.WriteString("session: " + session + "\n")
	sb.WriteString("time: \"2026-03-06T10:00:00+08:00 (Thursday, Asia/Shanghai, UTC+08:00)\"\n")
	sb.WriteString("delivery: " + delivery + "\n")
	sb.WriteString("visibility: user-visible\n")
	sb.WriteString("action: " + action + "\n")
	sb.WriteString("---\n\n")
	sb.WriteString(msg)
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

	// Count wake messages that still have thread field.
	trimmed, kept := 0, 0
	for _, m := range result {
		if m.Role != "user" || !strings.HasPrefix(m.Content, "---\n") {
			continue
		}
		if strings.Contains(m.Content, "thread:") {
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

	// Verify trimmed messages still have source, time, visibility; lost thread, session, delivery, action.
	for _, m := range result {
		if m.Role != "user" || strings.Contains(m.Content, "thread:") {
			continue
		}
		if !strings.Contains(m.Content, "source:") {
			t.Error("trimmed message lost source")
		}
		if !strings.Contains(m.Content, "time:") {
			t.Error("trimmed message lost time")
		}
		if !strings.Contains(m.Content, "visibility:") {
			t.Error("trimmed message lost visibility")
		}
		if strings.Contains(m.Content, "session:") {
			t.Error("trimmed message still has session")
		}
		if strings.Contains(m.Content, "delivery:") {
			t.Error("trimmed message still has delivery")
		}
		if strings.Contains(m.Content, "action:") {
			t.Error("trimmed message still has action")
		}
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
