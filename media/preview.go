// Package media provides fast multimedia preview using lightweight LLM calls.
//
// When a channel downloads an image, Preview() makes a quick LLM call to get a
// brief description, injected into the wake payload as a preview before the
// message body so the main LLM has immediate context without calling read_file.
//
// Audio is NOT handled here: voice clips are transcribed by the stateless
// `audio-preview` agent (thread.Manager.AudioPreview), which receives the audio
// natively in user content and persists each run for inspection. This package
// keeps the audio MediaType + tag formatters + DetectAudioMime so callers can
// build the audio marker and format the agent's transcription consistently.
//
// This is an addition, NOT a replacement — the existing read_file/imagereader
// flow stays intact. Previews are marked as "for reference only".
package media

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

// PreviewTimeout is the maximum time for a single preview LLM call.
const PreviewTimeout = 30 * time.Second

// MediaType classifies a media file for preview routing.
type MediaType int

const (
	MediaTypeImage MediaType = iota
	MediaTypeAudio
)

// previewCandidate defines a provider+model pair that can handle image preview.
type previewCandidate struct {
	ProviderName string
	ModelType    string
}

// imagePriority is the default priority chain for image preview.
// whatai sits at the end so it only activates when no higher-priority provider
// is configured.
var imagePriority = []previewCandidate{
	{ProviderName: "openrouter", ModelType: "google/gemini-3.1-flash-lite"},
	{ProviderName: "openai", ModelType: "gpt-5.4-nano"},
	{ProviderName: "anthropic", ModelType: "claude-haiku-4-5"},
	{ProviderName: "whatai", ModelType: "gpt-5.4-mini"},
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

// Preview generates a brief text description of the image at filePath.
// It selects the first available provider from the priority chain, makes a
// quick LLM call with an image marker, and returns the description.
//
// Audio is rejected: it is transcribed by the audio-preview agent, not here.
func (p *LLMPreviewer) Preview(ctx context.Context, filePath string, mediaType MediaType) (string, error) {
	if mediaType == MediaTypeAudio {
		return "", fmt.Errorf("audio preview is handled by the audio-preview agent, not media.Preview")
	}

	cfg := p.cfgFn()
	if cfg == nil {
		return "", fmt.Errorf("config unavailable")
	}

	candidates := imagePriority

	// Override: env var or config can force a specific provider/model.
	if override := previewOverride(cfg); override != nil {
		candidates = []previewCandidate{*override}
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
	prov := reg.Constructor(apiKey, apiBase, selectedCandidate.ModelType, selectedCandidate.ModelType, 1024, 0.3)

	// Apply timeout.
	ctx, cancel := context.WithTimeout(ctx, PreviewTimeout)
	defer cancel()

	start := time.Now()
	logger.Info("media preview starting",
		"provider", selectedCandidate.ProviderName,
		"model", selectedCandidate.ModelType,
		"file", filePath,
	)

	prompt, mimeType := buildImagePrompt(filePath)
	req := &provider.Request{
		Messages: []provider.Message{
			{
				Role:    "user",
				Content: prompt,
				Media:   []string{fmt.Sprintf("<<media:%s:%s>>", mimeType, filePath)},
			},
		},
	}
	result, err := prov.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("preview LLM call failed (%s/%s): %w", selectedCandidate.ProviderName, selectedCandidate.ModelType, err)
	}
	resp, err := result.Wait()
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
		"durationMs", time.Since(start).Milliseconds(),
		"preview", truncatePreview(content, 100),
		"tokens", resp.Usage.TotalTokens,
	)

	return content, nil
}

// buildImagePrompt returns the prompt text and MIME type for an image preview.
func buildImagePrompt(filePath string) (string, string) {
	mimeType := detectImageMime(filePath)
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return "Describe this image. ALWAYS start by stating what the image is and its context (e.g., \"a screenshot of an iOS music app showing...\", \"a photo of a street sign\", \"a chat screenshot from WeChat\"). Then describe key visual elements (layout, UI regions, objects, people, scene). When transcribing text, ALWAYS annotate each piece of text with its position or role in parentheses — e.g., \"00:03 (top-left status bar time)\", \"74 (track number)\", \"SCHUMANN (artist name)\", \"-0:21 (time remaining)\", \"Lossless (audio quality badge)\". Never output raw text without describing where it is or what it means. Output ONLY the description, nothing else.", mimeType
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

// previewOverride checks env var and config for an image preview provider/model
// override. Env: NAGOBOT_PREVIEW_IMAGE="provider/model". Config:
// thread.preview.image (same format). Env takes precedence over config.
func previewOverride(cfg *config.Config) *previewCandidate {
	var cfgVal string
	if cfg.Thread.Preview != nil {
		cfgVal = cfg.Thread.Preview.Image
	}

	raw := strings.TrimSpace(os.Getenv("NAGOBOT_PREVIEW_IMAGE"))
	if raw == "" {
		raw = strings.TrimSpace(cfgVal)
	}
	if raw == "" {
		return nil
	}

	// Parse "provider/model" — first segment is provider, rest is model.
	idx := strings.Index(raw, "/")
	if idx <= 0 {
		return nil
	}
	return &previewCandidate{ProviderName: raw[:idx], ModelType: raw[idx+1:]}
}
