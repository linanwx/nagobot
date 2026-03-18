package channel

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestTelegramReplyContext_TextMessage(t *testing.T) {
	m := &models.Message{
		Text: "Hello world",
		From: &models.User{FirstName: "Alice", LastName: "Smith"},
	}
	got := telegramReplyContext(m)
	want := "[Reply to Alice Smith]: Hello world"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTelegramReplyContext_CaptionMessage(t *testing.T) {
	m := &models.Message{
		Caption: "Photo caption",
		From:    &models.User{FirstName: "Bob"},
	}
	got := telegramReplyContext(m)
	want := "[Reply to Bob]: Photo caption"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTelegramReplyContext_NoText(t *testing.T) {
	m := &models.Message{
		From: &models.User{FirstName: "Charlie"},
	}
	got := telegramReplyContext(m)
	if got != "" {
		t.Errorf("expected empty string for message without text, got %q", got)
	}
}

func TestTelegramReplyContext_UsernameOnly(t *testing.T) {
	m := &models.Message{
		Text: "test",
		From: &models.User{Username: "jdoe"},
	}
	got := telegramReplyContext(m)
	want := "[Reply to jdoe]: test"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTelegramReplyContext_NoFrom(t *testing.T) {
	m := &models.Message{
		Text: "test",
	}
	got := telegramReplyContext(m)
	want := "[Reply to unknown]: test"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
