// Package media provides helpers for the upfront media preview: MIME detection
// for building "<<media:mime:path>>" markers, and tag formatting for injecting
// a preview result into the wake payload.
//
// The previews themselves are produced by stateless preview agents
// (thread.Manager.AudioPreview / ImagePreview), which receive the media
// natively in user content and persist each run for inspection. This package
// holds only the shared, provider-agnostic formatting/detection helpers.
package media

import (
	"fmt"
	"strings"
)

// MediaType classifies a media file for preview tag formatting.
type MediaType int

const (
	MediaTypeImage MediaType = iota
	MediaTypeAudio
)

// DetectImageMime returns the MIME type for an image file based on extension.
// Exported so the dispatcher can build the "<<media:mime:path>>" marker passed
// to the image-preview agent.
func DetectImageMime(path string) string {
	ext := strings.ToLower(extOf(path))
	mimes := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".png": "image/png", ".gif": "image/gif",
		".webp": "image/webp", ".bmp": "image/bmp",
	}
	if m, ok := mimes[ext]; ok {
		return m
	}
	return "image/jpeg"
}

// DetectAudioMime returns the MIME type for an audio file based on extension.
// Exported so the dispatcher can build the "<<media:mime:path>>" marker passed
// to the audio-preview agent.
func DetectAudioMime(path string) string {
	ext := strings.ToLower(extOf(path))
	mimes := map[string]string{
		".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg",
		".mp3": "audio/mpeg", ".wav": "audio/wav",
		".m4a": "audio/mp4", ".flac": "audio/flac", ".aac": "audio/aac",
	}
	if m, ok := mimes[ext]; ok {
		return m
	}
	return "audio/ogg"
}

// extOf returns the file extension including the dot.
func extOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' || path[i] == '\\' {
			break
		}
	}
	return ""
}

// FormatPreviewTag formats a preview result for the `media_preview` wake
// frontmatter field. The label names the media type, not the field — the
// field name already says "preview".
func FormatPreviewTag(description string, mediaType MediaType) string {
	switch mediaType {
	case MediaTypeAudio:
		return fmt.Sprintf("[audio transcription (for reference only — use read_file for detailed analysis): %s]", description)
	default:
		return fmt.Sprintf("[image (for reference only — use read_file for detailed analysis): %s]", description)
	}
}

// FormatPreviewError formats a preview error for injection into the wake payload.
func FormatPreviewError(err error, mediaType MediaType) string {
	switch mediaType {
	case MediaTypeAudio:
		return fmt.Sprintf("[audio_preview failed: %s]", err.Error())
	default:
		return fmt.Sprintf("[media_preview failed: %s]", err.Error())
	}
}
