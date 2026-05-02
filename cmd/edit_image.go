package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

const (
	openaiEditsEndpointDefault = "https://api.openai.com/v1/images/edits"
)

var (
	editImageImages      []string
	editImageMask        string
	editImagePrompt      string
	editImageProvider    string
	editImageModel       string
	editImageSize        string
	editImageQuality     string
	editImageFormat      string
	editImageCompression int
	editImageOutput      string
)

var editImageCmd = &cobra.Command{
	Use:     "edit-image --image <path> [--image <path>...] --prompt <text> [flags]",
	Short:   "Edit / compose with reference images via gpt-image-2 and save the result",
	GroupID: "internal",
	RunE:    runEditImage,
}

func init() {
	editImageCmd.Flags().StringArrayVar(&editImageImages, "image", nil, "Reference image path. Repeat for multiple references (image[] in the API).")
	editImageCmd.Flags().StringVar(&editImageMask, "mask", "", "Optional PNG mask. Transparent regions are the editable area. Applies to the first --image.")
	editImageCmd.Flags().StringVarP(&editImagePrompt, "prompt", "p", "", "Edit instruction (required). Refer to inputs as 'image 1', 'image 2', etc.")
	editImageCmd.Flags().StringVar(&editImageProvider, "provider", "openai", "openai (direct) | whatai (relay)")
	editImageCmd.Flags().StringVar(&editImageModel, "model", "gpt-image-2", "Image model name as understood by the chosen provider")
	editImageCmd.Flags().StringVar(&editImageSize, "size", "auto", "auto | 1024x1024 | 1536x1024 | 1024x1536")
	editImageCmd.Flags().StringVar(&editImageQuality, "quality", "auto", "auto | low | medium | high")
	editImageCmd.Flags().StringVar(&editImageFormat, "format", "png", "png | jpeg")
	editImageCmd.Flags().IntVar(&editImageCompression, "compression", -1, "Output compression 0-100 (jpeg only; -1 = omit)")
	editImageCmd.Flags().StringVarP(&editImageOutput, "output", "o", "", "Output file path. Default: {workspace}/media/edit-{ts}.{ext}")
	_ = editImageCmd.MarkFlagRequired("image")
	_ = editImageCmd.MarkFlagRequired("prompt")
	rootCmd.AddCommand(editImageCmd)
}

// editParams holds validated, provider-agnostic input for an edit call.
type editParams struct {
	Images      []string // file paths
	Mask        string   // file path or ""
	Prompt      string
	Model       string
	Size        string // "" if auto
	Quality     string // "" if auto
	Format      string // "png" or "jpeg"
	Compression int    // -1 if unset
}

// editResult mirrors imageGenResult but is local to the edit path.
type editResult struct {
	Bytes        []byte
	InputTokens  int
	OutputTokens int
}

func runEditImage(_ *cobra.Command, _ []string) error {
	prompt := strings.TrimSpace(editImagePrompt)
	if prompt == "" {
		return fmt.Errorf("--prompt must not be empty")
	}
	if len(editImageImages) == 0 {
		return fmt.Errorf("at least one --image is required")
	}
	for _, p := range editImageImages {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("image %q: %w", p, err)
		}
	}
	if editImageMask != "" {
		if _, err := os.Stat(editImageMask); err != nil {
			return fmt.Errorf("mask %q: %w", editImageMask, err)
		}
	}

	format := strings.ToLower(strings.TrimSpace(editImageFormat))
	switch format {
	case "png", "jpeg":
	case "jpg":
		format = "jpeg"
	default:
		return fmt.Errorf("--format must be png or jpeg (got %q)", editImageFormat)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	p := editParams{
		Images:      editImageImages,
		Mask:        editImageMask,
		Prompt:      prompt,
		Model:       editImageModel,
		Format:      format,
		Compression: editImageCompression,
	}
	if s := strings.TrimSpace(editImageSize); s != "" && s != "auto" {
		p.Size = s
	}
	if q := strings.TrimSpace(editImageQuality); q != "" && q != "auto" {
		p.Quality = q
	}

	provider := strings.ToLower(strings.TrimSpace(editImageProvider))
	var result editResult
	switch provider {
	case "openai":
		result, err = editViaOpenAI(cfg, p)
	case "whatai":
		result, err = editViaWhatAI(cfg, p)
	default:
		return fmt.Errorf("--provider must be openai or whatai (got %q)", editImageProvider)
	}
	if err != nil {
		return err
	}

	outPath, err := writeEditOutput(cfg, result.Bytes, format)
	if err != nil {
		return err
	}

	pairs := [][2]string{
		{"command", "edit-image"},
		{"status", "ok"},
		{"provider", provider},
		{"model", p.Model},
		{"images", fmt.Sprintf("%d", len(p.Images))},
		{"input_tokens", fmt.Sprintf("%d", result.InputTokens)},
		{"output_tokens", fmt.Sprintf("%d", result.OutputTokens)},
	}
	if p.Size != "" {
		pairs = append(pairs, [2]string{"size", p.Size})
	}
	if p.Quality != "" {
		pairs = append(pairs, [2]string{"quality", p.Quality})
	}
	pairs = append(pairs, [2]string{"format", format}, [2]string{"path", outPath})

	fmt.Print(tools.CmdOutput(pairs, outPath) + "\n")
	return nil
}

// ============================================================================
// Provider: openai (direct OpenAI API)
//
// Endpoint: POST /v1/images/edits, multipart/form-data.
// Multiple references go through repeated `image[]` parts. Returns the same
// JSON shape as generations: {data:[{b64_json:"..."}], usage:{...}}.
// ============================================================================

type openaiEditResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func editViaOpenAI(cfg *config.Config, p editParams) (editResult, error) {
	pc := cfg.Providers.OpenAI
	if pc == nil || pc.APIKey == "" {
		return editResult{}, fmt.Errorf("openai apiKey not configured (run `nagobot set-provider-key openai`)")
	}

	endpoint := openaiEditsEndpointDefault
	if base := strings.TrimRight(pc.APIBase, "/"); base != "" && base != openaiImagesAPIBaseDefault {
		endpoint = base + "/v1/images/edits"
	}

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	textFields := map[string]string{
		"model":  p.Model,
		"prompt": p.Prompt,
	}
	if p.Size != "" {
		textFields["size"] = p.Size
	}
	if p.Quality != "" {
		textFields["quality"] = p.Quality
	}
	textFields["output_format"] = p.Format
	if p.Format == "jpeg" && p.Compression >= 0 {
		if p.Compression > 100 {
			return editResult{}, fmt.Errorf("--compression must be 0-100")
		}
		textFields["output_compression"] = fmt.Sprintf("%d", p.Compression)
	}
	for k, v := range textFields {
		if err := mw.WriteField(k, v); err != nil {
			return editResult{}, fmt.Errorf("multipart field %s: %w", k, err)
		}
	}

	for i, path := range p.Images {
		if err := attachFile(mw, "image[]", path); err != nil {
			return editResult{}, fmt.Errorf("attach image[%d] %s: %w", i, path, err)
		}
	}
	if p.Mask != "" {
		if err := attachFile(mw, "mask", p.Mask); err != nil {
			return editResult{}, fmt.Errorf("attach mask %s: %w", p.Mask, err)
		}
	}
	if err := mw.Close(); err != nil {
		return editResult{}, fmt.Errorf("multipart close: %w", err)
	}

	respBytes, statusCode, err := doMultipartPOST(endpoint, pc.APIKey, mw.FormDataContentType(), body)
	if err != nil {
		return editResult{}, fmt.Errorf("openai request failed: %w", err)
	}

	var parsed openaiEditResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return editResult{}, fmt.Errorf("openai parse (HTTP %d): %w; body: %s",
			statusCode, err, truncateGenImg(string(respBytes), 400))
	}
	if statusCode != http.StatusOK {
		msg := "unknown error"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return editResult{}, fmt.Errorf("openai HTTP %d: %s", statusCode, msg)
	}
	if len(parsed.Data) == 0 || parsed.Data[0].B64JSON == "" {
		return editResult{}, fmt.Errorf("openai returned no image: %s", truncateGenImg(string(respBytes), 400))
	}
	raw, err := base64.StdEncoding.DecodeString(parsed.Data[0].B64JSON)
	if err != nil {
		return editResult{}, fmt.Errorf("openai b64 decode: %w", err)
	}
	return editResult{
		Bytes:        raw,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}, nil
}

// ============================================================================
// Provider: whatai (api.whatai.cc relay)
//
// Same multipart contract as openai, but we always set response_format=b64_json
// the same way we do on generations — the relay routes gpt-image-2 through
// DALL-E-3-style protocol where url is the default, b64_json is opt-in.
// ============================================================================

type whataiEditResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func editViaWhatAI(cfg *config.Config, p editParams) (editResult, error) {
	pc := cfg.Providers.WhatAI
	if pc == nil || pc.APIKey == "" {
		return editResult{}, fmt.Errorf("whatai apiKey not configured (add providers.whatai.apiKey to config.yaml)")
	}

	base := strings.TrimRight(pc.APIBase, "/")
	if base == "" {
		base = whatAIImagesAPIBase
	}
	endpoint := base + "/v1/images/edits"

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	textFields := map[string]string{
		"model":           p.Model,
		"prompt":          p.Prompt,
		"response_format": "b64_json",
	}
	if p.Size != "" {
		textFields["size"] = p.Size
	}
	if p.Quality != "" {
		textFields["quality"] = p.Quality
	}
	for k, v := range textFields {
		if err := mw.WriteField(k, v); err != nil {
			return editResult{}, fmt.Errorf("multipart field %s: %w", k, err)
		}
	}

	for i, path := range p.Images {
		if err := attachFile(mw, "image[]", path); err != nil {
			return editResult{}, fmt.Errorf("attach image[%d] %s: %w", i, path, err)
		}
	}
	if p.Mask != "" {
		if err := attachFile(mw, "mask", p.Mask); err != nil {
			return editResult{}, fmt.Errorf("attach mask %s: %w", p.Mask, err)
		}
	}
	if err := mw.Close(); err != nil {
		return editResult{}, fmt.Errorf("multipart close: %w", err)
	}

	respBytes, statusCode, err := doMultipartPOST(endpoint, pc.APIKey, mw.FormDataContentType(), body)
	if err != nil {
		return editResult{}, fmt.Errorf("whatai request failed: %w", err)
	}

	var parsed whataiEditResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return editResult{}, fmt.Errorf("whatai parse (HTTP %d): %w; body: %s",
			statusCode, err, truncateGenImg(string(respBytes), 400))
	}
	if statusCode != http.StatusOK {
		msg := "unknown error"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return editResult{}, fmt.Errorf("whatai HTTP %d: %s", statusCode, msg)
	}
	if len(parsed.Data) == 0 {
		return editResult{}, fmt.Errorf("whatai returned no image: %s", truncateGenImg(string(respBytes), 400))
	}
	d := parsed.Data[0]
	if d.B64JSON == "" {
		if d.URL != "" {
			return editResult{}, fmt.Errorf("whatai returned url despite response_format=b64_json: %s", d.URL)
		}
		return editResult{}, fmt.Errorf("whatai data[0] has neither b64_json nor url")
	}
	raw, err := base64.StdEncoding.DecodeString(d.B64JSON)
	if err != nil {
		return editResult{}, fmt.Errorf("whatai b64 decode: %w", err)
	}
	return editResult{
		Bytes:        raw,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}, nil
}

// ============================================================================
// Shared helpers
// ============================================================================

// attachFile writes a multipart file part with a real Content-Type derived
// from the file extension. multipart.Writer.CreateFormFile defaults to
// application/octet-stream which OpenAI's edits endpoint rejects.
func attachFile(mw *multipart.Writer, fieldName, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	ct := contentTypeForExt(filepath.Ext(path))
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, fieldName, filepath.Base(path)))
	h.Set("Content-Type", ct)
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, f)
	return err
}

func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func doMultipartPOST(endpoint, apiKey, contentType string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequest("POST", endpoint, body)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<27))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return respBytes, resp.StatusCode, nil
}

func writeEditOutput(cfg *config.Config, raw []byte, format string) (string, error) {
	ext := format
	if ext == "jpeg" {
		ext = "jpg"
	}
	var path string
	if editImageOutput != "" {
		path = editImageOutput
	} else {
		ws, err := cfg.WorkspacePath()
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
		mediaDir := filepath.Join(ws, "media")
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			return "", fmt.Errorf("create media dir: %w", err)
		}
		ts := time.Now().Format("20060102-150405")
		path = filepath.Join(mediaDir, fmt.Sprintf("edit-%s.%s", ts, ext))
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create output dir: %w", err)
		}
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}
