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
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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

// previewMode determines how the preview call is made.
type previewMode int

const (
	modeChat previewMode = iota // LLM Chat (provider.Chat)
	modeSTT                     // Speech-to-text (OpenAI /v1/audio/transcriptions)
)

// previewCandidate defines a provider+model pair that can handle a media type.
type previewCandidate struct {
	ProviderName string
	ModelType    string
	Mode         previewMode // default modeChat
}

// imagePriority is the default priority chain for image preview.
var imagePriority = []previewCandidate{
	{ProviderName: "openrouter", ModelType: "google/gemini-3.1-flash-lite-preview"},
	{ProviderName: "openai", ModelType: "gpt-5.4-nano"},
	{ProviderName: "anthropic", ModelType: "claude-haiku-4-5"},
}

// audioPriority is the priority chain for audio preview.
var audioPriority = []previewCandidate{
	{ProviderName: "openrouter", ModelType: "google/gemini-3.1-flash-lite-preview"},
	{ProviderName: "openai", ModelType: "gpt-4o-mini-transcribe", Mode: modeSTT},
	{ProviderName: "gemini", ModelType: "gemini-3.1-flash-lite-preview"},
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

	// Override: env var or config can force a specific provider/model.
	if override := previewOverride(cfg, mediaType); override != nil {
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
	prov := reg.Constructor(apiKey, apiBase, selectedCandidate.ModelType, selectedCandidate.ModelType, 256, 0.3)

	// Apply timeout.
	ctx, cancel := context.WithTimeout(ctx, PreviewTimeout)
	defer cancel()

	start := time.Now()
	logger.Info("media preview starting",
		"provider", selectedCandidate.ProviderName,
		"model", selectedCandidate.ModelType,
		"mode", previewModeLabel(selectedCandidate.Mode),
		"mediaType", mediaTypeLabel(mediaType),
		"file", filePath,
	)

	var content string
	var tokens int

	if selectedCandidate.Mode == modeSTT {
		// Speech-to-text: OpenAI /v1/audio/transcriptions endpoint.
		text, err := callSTT(ctx, apiKey, apiBase, selectedCandidate.ModelType, filePath)
		if err != nil {
			return "", fmt.Errorf("preview STT failed (%s/%s): %w", selectedCandidate.ProviderName, selectedCandidate.ModelType, err)
		}
		content = strings.TrimSpace(text)
	} else {
		// LLM Chat: send media via provider.Chat().
		prompt, mimeType := buildPreviewPrompt(filePath, mediaType)
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
		content = strings.TrimSpace(resp.Content)
		tokens = resp.Usage.TotalTokens
	}

	if content == "" {
		return "", fmt.Errorf("preview returned empty content (%s/%s)", selectedCandidate.ProviderName, selectedCandidate.ModelType)
	}

	logger.Info("media preview completed",
		"provider", selectedCandidate.ProviderName,
		"model", selectedCandidate.ModelType,
		"mode", previewModeLabel(selectedCandidate.Mode),
		"mediaType", mediaTypeLabel(mediaType),
		"durationMs", time.Since(start).Milliseconds(),
		"preview", truncatePreview(content, 100),
		"tokens", tokens,
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

func previewModeLabel(m previewMode) string {
	if m == modeSTT {
		return "stt"
	}
	return "chat"
}

// previewOverride checks env vars and config for a preview provider/model override.
// Env: NAGOBOT_PREVIEW_IMAGE="provider/model" or NAGOBOT_PREVIEW_AUDIO="provider/model"
// Config: thread.preview.image or thread.preview.audio (same format)
// Env takes precedence over config.
func previewOverride(cfg *config.Config, mediaType MediaType) *previewCandidate {
	var envKey, cfgVal string
	switch mediaType {
	case MediaTypeAudio:
		envKey = "NAGOBOT_PREVIEW_AUDIO"
		if cfg.Thread.Preview != nil {
			cfgVal = cfg.Thread.Preview.Audio
		}
	default:
		envKey = "NAGOBOT_PREVIEW_IMAGE"
		if cfg.Thread.Preview != nil {
			cfgVal = cfg.Thread.Preview.Image
		}
	}

	raw := strings.TrimSpace(os.Getenv(envKey))
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
	provName := raw[:idx]
	modelType := raw[idx+1:]

	c := &previewCandidate{ProviderName: provName, ModelType: modelType}
	// Auto-detect STT mode for known transcription models.
	if strings.Contains(modelType, "transcribe") {
		c.Mode = modeSTT
	}
	return c
}

// callSTT calls the OpenAI-compatible /v1/audio/transcriptions endpoint.
// The file is uploaded as multipart form data. .oga files are renamed to .ogg
// because OpenAI does not recognize the .oga extension.
func callSTT(ctx context.Context, apiKey, apiBase, model, filePath string) (string, error) {
	base := "https://api.openai.com/v1"
	if apiBase != "" {
		base = strings.TrimRight(apiBase, "/")
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer writer.Close()
		_ = writer.WriteField("model", model)
		// Use .ogg instead of .oga — OpenAI rejects .oga but accepts .ogg (same codec).
		uploadName := filepath.Base(filePath)
		if strings.HasSuffix(strings.ToLower(uploadName), ".oga") {
			uploadName = uploadName[:len(uploadName)-4] + ".ogg"
		}
		part, err := writer.CreateFormFile("file", uploadName)
		if err != nil {
			return
		}
		f, err := os.Open(filePath)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = io.Copy(part, f)
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", base+"/audio/transcriptions", pr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%d %s", resp.StatusCode, truncatePreview(string(body), 200))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	return result.Text, nil
}
