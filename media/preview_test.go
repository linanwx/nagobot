package media

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/config"
)

// mockPreviewer implements Previewer for testing.
type mockPreviewer struct {
	result string
	err    error
}

func (m *mockPreviewer) Preview(ctx context.Context, filePath string, mediaType MediaType) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}

func TestFormatPreviewTag_Image(t *testing.T) {
	tag := FormatPreviewTag("A screenshot of code with Python error", MediaTypeImage)
	if !strings.Contains(tag, "media_preview") {
		t.Errorf("image preview tag should contain 'media_preview', got: %s", tag)
	}
	if !strings.Contains(tag, "for reference only") {
		t.Errorf("image preview tag should contain 'for reference only', got: %s", tag)
	}
	if !strings.Contains(tag, "A screenshot of code") {
		t.Errorf("image preview tag should contain description, got: %s", tag)
	}
}

func TestFormatPreviewTag_Audio(t *testing.T) {
	tag := FormatPreviewTag("User says: help me debug this", MediaTypeAudio)
	if !strings.Contains(tag, "audio_preview") {
		t.Errorf("audio preview tag should contain 'audio_preview', got: %s", tag)
	}
	if !strings.Contains(tag, "for reference only") {
		t.Errorf("audio preview tag should contain 'for reference only', got: %s", tag)
	}
}

func TestFormatPreviewError_Image(t *testing.T) {
	tag := FormatPreviewError(fmt.Errorf("no provider available"), MediaTypeImage)
	if !strings.Contains(tag, "media_preview failed") {
		t.Errorf("image error tag should contain 'media_preview failed', got: %s", tag)
	}
	if !strings.Contains(tag, "no provider available") {
		t.Errorf("image error tag should contain error message, got: %s", tag)
	}
}

func TestFormatPreviewError_Audio(t *testing.T) {
	tag := FormatPreviewError(fmt.Errorf("timeout"), MediaTypeAudio)
	if !strings.Contains(tag, "audio_preview failed") {
		t.Errorf("audio error tag should contain 'audio_preview failed', got: %s", tag)
	}
}

func TestDetectImageMime(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"photo.png", "image/png"},
		{"photo.gif", "image/gif"},
		{"photo.webp", "image/webp"},
		{"photo.bmp", "image/bmp"},
		{"photo.unknown", "image/jpeg"}, // default fallback
	}
	for _, tt := range tests {
		got := detectImageMime(tt.path)
		if got != tt.want {
			t.Errorf("detectImageMime(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestDetectAudioMime(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"voice.ogg", "audio/ogg"},
		{"voice.oga", "audio/ogg"},
		{"voice.opus", "audio/ogg"},
		{"voice.mp3", "audio/mpeg"},
		{"voice.wav", "audio/wav"},
		{"voice.m4a", "audio/mp4"},
		{"voice.flac", "audio/flac"},
		{"voice.aac", "audio/aac"},
		{"voice.unknown", "audio/ogg"}, // default fallback
	}
	for _, tt := range tests {
		got := detectAudioMime(tt.path)
		if got != tt.want {
			t.Errorf("detectAudioMime(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestBuildPreviewPrompt_Image(t *testing.T) {
	prompt, mime := buildPreviewPrompt("photo.png", MediaTypeImage)
	if !strings.Contains(prompt, "Describe") {
		t.Errorf("image prompt should contain 'Describe', got: %s", prompt)
	}
	if mime != "image/png" {
		t.Errorf("expected image/png, got: %s", mime)
	}
}

func TestBuildPreviewPrompt_Audio(t *testing.T) {
	prompt, mime := buildPreviewPrompt("voice.ogg", MediaTypeAudio)
	if !strings.Contains(prompt, "Transcribe") {
		t.Errorf("audio prompt should contain 'Transcribe', got: %s", prompt)
	}
	if mime != "audio/ogg" {
		t.Errorf("expected audio/ogg, got: %s", mime)
	}
}

func TestMockPreviewer(t *testing.T) {
	// Test successful preview
	p := &mockPreviewer{result: "A cat sitting on a keyboard"}
	desc, err := p.Preview(context.Background(), "/tmp/photo.jpg", MediaTypeImage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "A cat sitting on a keyboard" {
		t.Errorf("unexpected description: %s", desc)
	}

	// Test error preview
	p = &mockPreviewer{err: fmt.Errorf("provider unavailable")}
	_, err = p.Preview(context.Background(), "/tmp/photo.jpg", MediaTypeImage)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("expected 'provider unavailable' in error, got: %v", err)
	}
}

func TestExtOf(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/tmp/photo.jpg", ".jpg"},
		{"/tmp/voice.ogg", ".ogg"},
		{"/tmp/noext", ""},
		{"simple.png", ".png"},
		{"/path/to/file.tar.gz", ".gz"},
	}
	for _, tt := range tests {
		got := extOf(tt.path)
		if got != tt.want {
			t.Errorf("extOf(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestTruncatePreview(t *testing.T) {
	short := "hello"
	if got := truncatePreview(short, 10); got != short {
		t.Errorf("truncatePreview(%q, 10) = %q, want %q", short, got, short)
	}

	long := "this is a very long description that exceeds the limit"
	got := truncatePreview(long, 20)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated string should end with '...', got: %s", got)
	}
	if len(got) != 23 { // 20 + "..."
		t.Errorf("expected length 23, got %d: %s", len(got), got)
	}
}

func TestMediaTypeLabel(t *testing.T) {
	if got := mediaTypeLabel(MediaTypeImage); got != "image" {
		t.Errorf("expected 'image', got: %s", got)
	}
	if got := mediaTypeLabel(MediaTypeAudio); got != "audio" {
		t.Errorf("expected 'audio', got: %s", got)
	}
}

func TestLLMPreviewer_NilConfig(t *testing.T) {
	p := NewPreviewer(func() *config.Config { return nil })
	_, err := p.Preview(context.Background(), "/tmp/photo.jpg", MediaTypeImage)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config unavailable") {
		t.Errorf("expected 'config unavailable', got: %v", err)
	}
}

func TestLLMPreviewer_NoProviderAvailable(t *testing.T) {
	// Config with no provider keys configured — should fail gracefully.
	p := NewPreviewer(func() *config.Config {
		return &config.Config{}
	})
	_, err := p.Preview(context.Background(), "/tmp/photo.jpg", MediaTypeImage)
	if err == nil {
		t.Fatal("expected error when no provider is available")
	}
	if !strings.Contains(err.Error(), "no preview provider available") {
		t.Errorf("expected 'no preview provider available', got: %v", err)
	}
}
