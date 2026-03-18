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
	// truncate(500) + "..." = 503 chars for the reply context portion
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
	// Should have both reply context and sender prefix
	if !strings.Contains(got, "[Reply to Alice]: Some point") {
		t.Errorf("missing reply context: %s", got)
	}
	if !strings.Contains(got, "[Bob]:") {
		t.Errorf("missing sender prefix: %s", got)
	}
}
