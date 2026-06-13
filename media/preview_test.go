package media

import (
	"fmt"
	"strings"
	"testing"
)

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
		if got := DetectImageMime(tt.path); got != tt.want {
			t.Errorf("DetectImageMime(%q) = %q, want %q", tt.path, got, tt.want)
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
		if got := DetectAudioMime(tt.path); got != tt.want {
			t.Errorf("DetectAudioMime(%q) = %q, want %q", tt.path, got, tt.want)
		}
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
		if got := extOf(tt.path); got != tt.want {
			t.Errorf("extOf(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
