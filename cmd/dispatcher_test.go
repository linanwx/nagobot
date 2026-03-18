package cmd

import (
	"strings"
	"testing"

	"github.com/linanwx/nagobot/channel"
)

func TestPreprocessMessage_ReplyContext(t *testing.T) {
	d := &Dispatcher{}
	msg := &channel.Message{
		Text: "What do you think?",
		Metadata: map[string]string{
			"reply_context": "[Reply to Alice]: Original message here",
		},
	}
	got := d.preprocessMessage(msg)
	if !strings.Contains(got, "[Reply to Alice]: Original message here") {
		t.Errorf("reply context not found in output: %s", got)
	}
	if !strings.Contains(got, "What do you think?") {
		t.Errorf("user message not found in output: %s", got)
	}
	// reply_context should come before user text
	idx1 := strings.Index(got, "[Reply to Alice]")
	idx2 := strings.Index(got, "What do you think?")
	if idx1 > idx2 {
		t.Errorf("reply context should appear before user message")
	}
}

func TestPreprocessMessage_ReplyContextTruncated(t *testing.T) {
	d := &Dispatcher{}
	longContent := strings.Repeat("x", 600)
	msg := &channel.Message{
		Text: "reply",
		Metadata: map[string]string{
			"reply_context": longContent,
		},
	}
	got := d.preprocessMessage(msg)
	if strings.Contains(got, longContent) {
		t.Errorf("reply context should have been truncated")
	}
	if !strings.Contains(got, "...") {
		t.Errorf("truncated reply context should end with ellipsis")
	}
}

func TestPreprocessMessage_NoReplyContext(t *testing.T) {
	d := &Dispatcher{}
	msg := &channel.Message{
		Text:     "Hello",
		Metadata: map[string]string{},
	}
	got := d.preprocessMessage(msg)
	if got != "Hello" {
		t.Errorf("expected plain text, got %q", got)
	}
}

func TestPreprocessMessage_ReplyWithGroupSender(t *testing.T) {
	d := &Dispatcher{}
	msg := &channel.Message{
		Text:     "I agree",
		Username: "Bob",
		Metadata: map[string]string{
			"reply_context": "[Reply to Alice]: Some point",
			"chat_type":     "group",
		},
	}
	got := d.preprocessMessage(msg)
	if !strings.Contains(got, "[Reply to Alice]: Some point") {
		t.Errorf("missing reply context: %s", got)
	}
	if !strings.Contains(got, "[Bob]:") {
		t.Errorf("missing sender prefix: %s", got)
	}
}

func TestTruncate_RuneSafe(t *testing.T) {
	// Chinese characters: each is one rune but 3 bytes
	input := strings.Repeat("中", 600)
	got := truncate(input, 500)
	runes := []rune(got)
	// Should be at most 500 runes + "..." (3 runes)
	if len(runes) > 503 {
		t.Errorf("truncated result has %d runes, expected <= 503", len(runes))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated result should end with '...': %s", got[len(got)-20:])
	}
}

func TestTruncate_SentenceBoundary(t *testing.T) {
	// 490 chars + period + 109 more chars = well over 500
	input := strings.Repeat("a", 490) + "." + strings.Repeat("b", 109)
	got := truncate(input, 500)
	// Should cut at the period (position 491), not at 500
	if !strings.HasSuffix(got, "...") {
		t.Errorf("should end with ellipsis")
	}
	// The period should be included, and b's should not
	if strings.Contains(got, "b") {
		t.Errorf("should have cut at sentence boundary before b's: %s", got)
	}
}

func TestTruncate_ChineseSentenceBoundary(t *testing.T) {
	input := strings.Repeat("中", 480) + "。" + strings.Repeat("文", 100)
	got := truncate(input, 500)
	if strings.Contains(got, "文") {
		t.Errorf("should have cut at 。boundary")
	}
}

func TestTruncate_NoTruncationNeeded(t *testing.T) {
	input := "short message"
	got := truncate(input, 500)
	if got != input {
		t.Errorf("should not truncate short messages, got %q", got)
	}
}
