package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/media"
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

// testPreviewer implements media.Previewer for testing.
type testPreviewer struct {
	results map[string]string // filePath -> description
	errs    map[string]error  // filePath -> error
}

func (p *testPreviewer) Preview(_ context.Context, filePath string, _ media.MediaType) (string, error) {
	if err, ok := p.errs[filePath]; ok {
		return "", err
	}
	if desc, ok := p.results[filePath]; ok {
		return desc, nil
	}
	return "", fmt.Errorf("unexpected file: %s", filePath)
}

func TestGenerateMediaPreviews_ImagePath(t *testing.T) {
	d := &Dispatcher{
		previewer: &testPreviewer{
			results: map[string]string{
				"/tmp/media/img-20260322-120000-abcd.jpg": "A cat sitting on a keyboard",
			},
		},
	}
	summary := "[Media: photo]\nimage_path: /tmp/media/img-20260322-120000-abcd.jpg"
	got := d.generateMediaPreviews(summary)
	if !strings.Contains(got, "media_preview") {
		t.Errorf("expected media_preview tag, got: %s", got)
	}
	if !strings.Contains(got, "A cat sitting on a keyboard") {
		t.Errorf("expected preview description, got: %s", got)
	}
}

func TestGenerateMediaPreviews_AudioPath(t *testing.T) {
	d := &Dispatcher{
		previewer: &testPreviewer{
			results: map[string]string{
				"/tmp/media/audio-20260322-120000-abcd.ogg": "Hello, can you help me?",
			},
		},
	}
	summary := "[Media: voice]\naudio_path: /tmp/media/audio-20260322-120000-abcd.ogg\nduration: 5s"
	got := d.generateMediaPreviews(summary)
	if !strings.Contains(got, "audio_preview") {
		t.Errorf("expected audio_preview tag, got: %s", got)
	}
	if !strings.Contains(got, "Hello, can you help me?") {
		t.Errorf("expected transcription, got: %s", got)
	}
}

func TestGenerateMediaPreviews_PreviewError(t *testing.T) {
	d := &Dispatcher{
		previewer: &testPreviewer{
			errs: map[string]error{
				"/tmp/media/img.jpg": fmt.Errorf("timeout"),
			},
		},
	}
	summary := "[Media: photo]\nimage_path: /tmp/media/img.jpg"
	got := d.generateMediaPreviews(summary)
	if !strings.Contains(got, "media_preview failed") {
		t.Errorf("expected error tag, got: %s", got)
	}
	if !strings.Contains(got, "timeout") {
		t.Errorf("expected error message, got: %s", got)
	}
}

func TestGenerateMediaPreviews_NoMediaPaths(t *testing.T) {
	d := &Dispatcher{
		previewer: &testPreviewer{},
	}
	// Summary without image_path or audio_path
	summary := "[Media: sticker]\nemoji: 😀\nsticker_set: MyStickers"
	got := d.generateMediaPreviews(summary)
	if got != "" {
		t.Errorf("expected empty string for non-media summary, got: %s", got)
	}
}

func TestGenerateMediaPreviews_NilPreviewer(t *testing.T) {
	d := &Dispatcher{previewer: nil}
	got := d.generateMediaPreviews("[Media: photo]\nimage_path: /tmp/photo.jpg")
	if got != "" {
		t.Errorf("expected empty string for nil previewer, got: %s", got)
	}
}

func TestGenerateMediaPreviews_MultipleMedia(t *testing.T) {
	d := &Dispatcher{
		previewer: &testPreviewer{
			results: map[string]string{
				"/tmp/media/img1.jpg":   "First image",
				"/tmp/media/audio1.ogg": "Audio content",
			},
		},
	}
	summary := "[Media: photo]\nimage_path: /tmp/media/img1.jpg\n\n[Media: voice]\naudio_path: /tmp/media/audio1.ogg"
	got := d.generateMediaPreviews(summary)
	if !strings.Contains(got, "media_preview") {
		t.Errorf("expected media_preview tag, got: %s", got)
	}
	if !strings.Contains(got, "audio_preview") {
		t.Errorf("expected audio_preview tag, got: %s", got)
	}
}

func TestPreprocessMessage_WithMediaPreview(t *testing.T) {
	d := &Dispatcher{
		previewer: &testPreviewer{
			results: map[string]string{
				"/tmp/media/img.jpg": "A code screenshot",
			},
		},
	}
	msg := &channel.Message{
		Text: "What's this?",
		Metadata: map[string]string{
			"media_summary": "[Media: photo]\nimage_path: /tmp/media/img.jpg",
		},
	}
	got := d.preprocessMessage(msg)
	// Order: preview, then media_summary, then text
	previewIdx := strings.Index(got, "media_preview")
	summaryIdx := strings.Index(got, "[Media: photo]")
	textIdx := strings.Index(got, "What's this?")
	if previewIdx < 0 || summaryIdx < 0 || textIdx < 0 {
		t.Fatalf("missing expected content: %s", got)
	}
	if previewIdx >= summaryIdx {
		t.Errorf("preview should come before media_summary")
	}
	if summaryIdx >= textIdx {
		t.Errorf("media_summary should come before user text")
	}
}
