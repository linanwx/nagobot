// Package media provides fast multimedia preview using lightweight LLM calls.
//
// When a channel downloads an image or audio file, Preview() makes a quick
// (1-2s) LLM call to get a brief description. The result is injected into the
// wake payload as a preview before the message body, giving the main LLM
// immediate context about the media content without needing to call read_file.
//
// This is an addition, NOT a replacement — the existing read_file/imagereader
// flow stays intact. Previews are marked as "for reference only".
package media

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

// PreviewTimeout is the maximum time for a single preview LLM call.
const PreviewTimeout = 5 * time.Second

// MediaType classifies a media file for preview routing.
type MediaType int

const (
	MediaTypeImage MediaType = iota
	MediaTypeAudio
)

// previewCandidate defines a provider+model pair that can handle a media type.
type previewCandidate struct {
	ProviderName string
	ModelType    string
}

// imagePriority is the priority chain for image preview.
// 1. Gemini Flash Lite (direct)
// 2. Claude Haiku (Anthropic direct)
var imagePriority = []previewCandidate{
	{ProviderName: "gemini", ModelType: "gemini-3.1-flash-lite-preview"},
	{ProviderName: "anthropic", ModelType: "claude-haiku-4-5"},
}

// audioPriority is the priority chain for audio preview.
// 1. Gemini Flash Lite (direct)
// 2. Gemini Flash Lite via OpenRouter
var audioPriority = []previewCandidate{
	{ProviderName: "gemini", ModelType: "gemini-3.1-flash-lite-preview"},
	{ProviderName: "openrouter", ModelType: "google/gemini-3.1-flash-lite"},
}

// Previewer generates quick media previews using lightweight LLM calls.
type Previewer interface {
	// Preview generates a brief text description of the media file.
	// Returns the description or an error string on failure.
	Preview(ctx context.Context, filePath string, mediaType MediaType) (string, error)
}

// LLMPreviewer implements Previewer using LLM provider calls.
type LLMPreviewer struct {
	cfgFn func() *config.Config
}

// NewPreviewer creates a new LLMPreviewer.
// cfgFn is called on each Preview() to get the latest config (hot-reload support).
func NewPreviewer(cfgFn func() *config.Config) *LLMPreviewer {
	return &LLMPreviewer{cfgFn: cfgFn}
}

// Preview generates a brief text description of the media file at filePath.
// It selects the first available provider from the priority chain, makes a
// quick LLM call with a media marker, and returns the description.
// On failure, returns an error (caller should inject error into prompt).
func (p *LLMPreviewer) Preview(ctx context.Context, filePath string, mediaType MediaType) (string, error) {
	cfg := p.cfgFn()
	if cfg == nil {
		return "", fmt.Errorf("config unavailable")
	}

	candidates := imagePriority
	if mediaType == MediaTypeAudio {
		candidates = audioPriority
	}

	// Find first available provider.
	var selectedCandidate *previewCandidate
	for i := range candidates {
		c := &candidates[i]
		if provider.ProviderKeyAvailable(cfg, c.ProviderName) {
			selectedCandidate = c
			break
		}
	}
	if selectedCandidate == nil {
		return "", fmt.Errorf("no preview provider available (no API keys configured for any preview-capable provider)")
	}

	// Build the provider instance.
	reg, ok := provider.GetProviderRegistration(selectedCandidate.ProviderName)
	if !ok || reg.Constructor == nil {
		return "", fmt.Errorf("preview provider %s not registered", selectedCandidate.ProviderName)
	}
	apiKey := provider.ProviderAPIKeyForPreview(cfg, selectedCandidate.ProviderName)
	if apiKey == "" {
		return "", fmt.Errorf("API key empty for preview provider %s", selectedCandidate.ProviderName)
	}
	apiBase := provider.ProviderAPIBaseForPreview(cfg, selectedCandidate.ProviderName)
	prov := reg.Constructor(apiKey, apiBase, selectedCandidate.ModelType, selectedCandidate.ModelType, 256, 0.3)

	// Build prompt.
	prompt, mimeType := buildPreviewPrompt(filePath, mediaType)

	// Apply timeout.
	ctx, cancel := context.WithTimeout(ctx, PreviewTimeout)
	defer cancel()

	req := &provider.Request{
		Messages: []provider.Message{
			provider.UserMessage(fmt.Sprintf("<<media:%s:%s>>\n\n%s", mimeType, filePath, prompt)),
		},
	}

	logger.Info("media preview starting",
		"provider", selectedCandidate.ProviderName,
		"model", selectedCandidate.ModelType,
		"mediaType", mediaTypeLabel(mediaType),
		"file", filePath,
	)

	resp, err := prov.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("preview LLM call failed (%s/%s): %w", selectedCandidate.ProviderName, selectedCandidate.ModelType, err)
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", fmt.Errorf("preview returned empty content (%s/%s)", selectedCandidate.ProviderName, selectedCandidate.ModelType)
	}

	logger.Info("media preview completed",
		"provider", selectedCandidate.ProviderName,
		"model", selectedCandidate.ModelType,
		"mediaType", mediaTypeLabel(mediaType),
		"preview", truncatePreview(content, 100),
		"tokens", resp.Usage.TotalTokens,
	)

	return content, nil
}

// buildPreviewPrompt returns the prompt text and MIME type for the preview call.
func buildPreviewPrompt(filePath string, mediaType MediaType) (string, string) {
	switch mediaType {
	case MediaTypeAudio:
		mimeType := detectAudioMime(filePath)
		if mimeType == "" {
			mimeType = "audio/ogg"
		}
		return "Transcribe this audio in one paragraph. Output ONLY the transcription, nothing else.", mimeType
	default: // image
		mimeType := detectImageMime(filePath)
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		return "Describe this image in one sentence. Be concise and factual. Output ONLY the description, nothing else.", mimeType
	}
}

// detectImageMime returns the MIME type for an image file based on extension.
func detectImageMime(path string) string {
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

// detectAudioMime returns the MIME type for an audio file based on extension.
func detectAudioMime(path string) string {
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

// FormatPreviewTag formats a preview result for injection into the wake payload.
func FormatPreviewTag(description string, mediaType MediaType) string {
	switch mediaType {
	case MediaTypeAudio:
		return fmt.Sprintf("[audio_preview (for reference only — use read_file for detailed analysis): %s]", description)
	default:
		return fmt.Sprintf("[media_preview (for reference only — use read_file for detailed analysis): %s]", description)
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

func mediaTypeLabel(mt MediaType) string {
	switch mt {
	case MediaTypeAudio:
		return "audio"
	default:
		return "image"
	}
}

func truncatePreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
