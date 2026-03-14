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

func TestCompressTier1_WakeTrim(t *testing.T) {
	// Build 5 wake messages + assistant replies.
	var messages []provider.Message
	for i := 0; i < 5; i++ {
		messages = append(messages,
			provider.Message{Role: "user", Content: makeWakeContent("telegram", "t-1", "telegram:123", "telegram:123", "A user sent a message.", "hello")},
			provider.Message{Role: "assistant", Content: "hi"},
		)
	}

	modified, result := compressTier1(messages, 3)
	if !modified {
		t.Fatal("expected modified=true")
	}

	// Content must never be modified.
	for i, m := range result {
		if m.Content != messages[i].Content {
			t.Errorf("message %d: Content was modified", i)
		}
	}

	// Count wake messages whose Compressed strips redundant fields.
	trimmed, kept := 0, 0
	for _, m := range result {
		if m.Role != "user" || !strings.HasPrefix(m.Content, "---\n") {
			continue
		}
		if m.Compressed == "" {
			kept++ // no compression = still intact (protected)
		} else {
			trimmed++
			// Compressed must not contain thread/session/delivery/action.
			if strings.Contains(m.Compressed, "thread:") {
				t.Error("trimmed Compressed still has thread field")
			}
			if strings.Contains(m.Compressed, "session:") {
				t.Error("trimmed Compressed still has session field")
			}
			if strings.Contains(m.Compressed, "delivery:") {
				t.Error("trimmed Compressed still has delivery field")
			}
			if strings.Contains(m.Compressed, "action:") {
				t.Error("trimmed Compressed still has action field")
			}
			// Must preserve source, time, visibility.
			if !strings.Contains(m.Compressed, "source:") {
				t.Error("trimmed Compressed lost source")
			}
			if !strings.Contains(m.Compressed, "time:") {
				t.Error("trimmed Compressed lost time")
			}
			if !strings.Contains(m.Compressed, "visibility:") {
				t.Error("trimmed Compressed lost visibility")
			}
		}
	}
	// Protection is based on last 3 assistant turns (not last 3 wake messages).
	// With 5 wake+assistant pairs and keepLast=3: messages 0-4 are outside protection,
	// so wake messages at idx 0, 2, 4 get trimmed; wake messages at idx 6, 8 are kept.
	if kept != 2 {
		t.Errorf("expected 2 kept wake messages, got %d", kept)
	}
	if trimmed != 3 {
		t.Errorf("expected 3 trimmed wake messages, got %d", trimmed)
	}
}

func TestCompressTier1_WakeTrimIdempotent(t *testing.T) {
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

	_, result := compressTier1(messages, 3)
	modified2, _ := compressTier1(result, 3)
	if modified2 {
		t.Error("second pass should not modify anything (idempotent)")
	}
}
