package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

const (
	openaiImagesEndpointDefault = "https://api.openai.com/v1/images/generations"
	openaiImagesAPIBaseDefault  = "https://api.openai.com"
)

var (
	genImagePrompt      string
	genImageProvider    string
	genImageModel       string
	genImageSize        string
	genImageQuality     string
	genImageFormat      string
	genImageCompression int
	genImageN           int
	genImageOutput      string
)

var generateImageCmd = &cobra.Command{
	Use:     "generate-image --prompt <text> [flags]",
	Short:   "Generate an image (gpt-image-2) and save it to disk",
	GroupID: "internal",
	RunE:    runGenerateImage,
}

func init() {
	generateImageCmd.Flags().StringVarP(&genImagePrompt, "prompt", "p", "", "Image prompt (required)")
	generateImageCmd.Flags().StringVar(&genImageProvider, "provider", "openai", "openai (direct API)")
	generateImageCmd.Flags().StringVar(&genImageModel, "model", "gpt-image-2", "Image model name as understood by the chosen provider")
	generateImageCmd.Flags().StringVar(&genImageSize, "size", "auto", "auto | 1024x1024 | 1536x1024 | 1024x1536")
	generateImageCmd.Flags().StringVar(&genImageQuality, "quality", "auto", "auto | low | medium | high")
	generateImageCmd.Flags().StringVar(&genImageFormat, "format", "png", "png | jpeg (gpt-image-2 ignores webp and returns png)")
	generateImageCmd.Flags().IntVar(&genImageCompression, "compression", -1, "Output compression 0-100 (jpeg only; -1 = omit)")
	generateImageCmd.Flags().IntVarP(&genImageN, "n", "n", 1, "Number of images to generate (1-10)")
	generateImageCmd.Flags().StringVarP(&genImageOutput, "output", "o", "", "Output file path. Default: {workspace}/media/img-{ts}.{ext}; with n>1 the index is appended.")
	_ = generateImageCmd.MarkFlagRequired("prompt")
	rootCmd.AddCommand(generateImageCmd)
}

// imageGenParams is the validated, provider-agnostic input.
type imageGenParams struct {
	Model       string
	Prompt      string
	N           int
	Size        string // "" if auto
	Quality     string // "" if auto
	Format      string // "png" or "jpeg"
	Compression int    // -1 if unset; only applies to jpeg
}

// imageGenResult is what each provider implementation returns. Bytes are raw
// decoded image bytes ready to write to disk; token counts are best-effort.
type imageGenResult struct {
	Bytes        [][]byte
	InputTokens  int
	OutputTokens int
}

func runGenerateImage(_ *cobra.Command, _ []string) error {
	prompt := strings.TrimSpace(genImagePrompt)
	if prompt == "" {
		return fmt.Errorf("--prompt must not be empty")
	}
	if genImageN < 1 || genImageN > 10 {
		return fmt.Errorf("--n must be between 1 and 10")
	}

	format := strings.ToLower(strings.TrimSpace(genImageFormat))
	switch format {
	case "png", "jpeg":
	case "jpg":
		format = "jpeg"
	default:
		return fmt.Errorf("--format must be png or jpeg (got %q)", genImageFormat)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	params := imageGenParams{
		Model:       genImageModel,
		Prompt:      prompt,
		N:           genImageN,
		Format:      format,
		Compression: genImageCompression,
	}
	if s := strings.TrimSpace(genImageSize); s != "" && s != "auto" {
		params.Size = s
	}
	if q := strings.TrimSpace(genImageQuality); q != "" && q != "auto" {
		params.Quality = q
	}

	provider := strings.ToLower(strings.TrimSpace(genImageProvider))
	var result imageGenResult
	switch provider {
	case "openai":
		result, err = generateViaOpenAI(cfg, params)
	default:
		return fmt.Errorf("--provider must be openai (got %q)", genImageProvider)
	}
	if err != nil {
		return err
	}

	outputs, err := writeImages(cfg, result.Bytes, format)
	if err != nil {
		return err
	}

	pairs := [][2]string{
		{"command", "generate-image"},
		{"status", "ok"},
		{"provider", provider},
		{"model", genImageModel},
		{"count", fmt.Sprintf("%d", len(outputs))},
		{"input_tokens", fmt.Sprintf("%d", result.InputTokens)},
		{"output_tokens", fmt.Sprintf("%d", result.OutputTokens)},
	}
	if params.Size != "" {
		pairs = append(pairs, [2]string{"size", params.Size})
	}
	if params.Quality != "" {
		pairs = append(pairs, [2]string{"quality", params.Quality})
	}
	pairs = append(pairs, [2]string{"format", format})
	for i, p := range outputs {
		pairs = append(pairs, [2]string{fmt.Sprintf("path_%d", i), p})
	}

	fmt.Print(tools.CmdOutput(pairs, strings.Join(outputs, "\n")) + "\n")
	return nil
}

// ============================================================================
// Provider: openai (direct OpenAI API)
//
// Contract: gpt-image-2 always returns b64_json. response_format is rejected
// (HTTP 400 "Unknown parameter"). Honors output_format/output_compression.
// ============================================================================

type openaiImgRequest struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	N                 int    `json:"n,omitempty"`
	Size              string `json:"size,omitempty"`
	Quality           string `json:"quality,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
}

type openaiImgResponse struct {
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

func generateViaOpenAI(cfg *config.Config, p imageGenParams) (imageGenResult, error) {
	pc := cfg.Providers.OpenAI
	if pc == nil || pc.APIKey == "" {
		return imageGenResult{}, fmt.Errorf("openai apiKey not configured (run `nagobot set-provider-key openai`)")
	}

	endpoint := openaiImagesEndpointDefault
	if base := strings.TrimRight(pc.APIBase, "/"); base != "" && base != openaiImagesAPIBaseDefault {
		endpoint = base + "/v1/images/generations"
	}

	req := openaiImgRequest{Model: p.Model, Prompt: p.Prompt, N: p.N, Size: p.Size, Quality: p.Quality}
	if p.Format == "jpeg" {
		req.OutputFormat = "jpeg"
	} else {
		req.OutputFormat = "png"
	}
	if p.Format == "jpeg" && p.Compression >= 0 {
		if p.Compression > 100 {
			return imageGenResult{}, fmt.Errorf("--compression must be 0-100")
		}
		c := p.Compression
		req.OutputCompression = &c
	}

	respBytes, statusCode, err := doImagePOST(endpoint, pc.APIKey, req)
	if err != nil {
		return imageGenResult{}, fmt.Errorf("openai request failed: %w", err)
	}

	var parsed openaiImgResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return imageGenResult{}, fmt.Errorf("openai parse (HTTP %d): %w; body: %s",
			statusCode, err, truncateGenImg(string(respBytes), 400))
	}
	if statusCode != http.StatusOK {
		msg := "unknown error"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return imageGenResult{}, fmt.Errorf("openai HTTP %d: %s", statusCode, msg)
	}
	if len(parsed.Data) == 0 {
		return imageGenResult{}, fmt.Errorf("openai returned no images: %s", truncateGenImg(string(respBytes), 400))
	}

	out := imageGenResult{
		Bytes:        make([][]byte, 0, len(parsed.Data)),
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}
	for i, d := range parsed.Data {
		if d.B64JSON == "" {
			return imageGenResult{}, fmt.Errorf("openai data[%d].b64_json is empty", i)
		}
		raw, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return imageGenResult{}, fmt.Errorf("openai data[%d] b64 decode: %w", i, err)
		}
		out.Bytes = append(out.Bytes, raw)
	}
	return out, nil
}

// ============================================================================
// Shared helpers
// ============================================================================

// doImagePOST marshals payload, posts to endpoint with bearer auth, returns
// raw response bytes and HTTP status code. The 5-min timeout matches the
// worst-case latency for high-quality non-square gpt-image-2 generations.
func doImagePOST(endpoint, apiKey string, payload any) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<27)) // 128 MiB cap
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return respBytes, resp.StatusCode, nil
}

func writeImages(cfg *config.Config, datas [][]byte, format string) ([]string, error) {
	ext := format
	if ext == "jpeg" {
		ext = "jpg"
	}

	var basePath string
	if genImageOutput != "" {
		basePath = genImageOutput
	} else {
		ws, err := cfg.WorkspacePath()
		if err != nil {
			return nil, fmt.Errorf("resolve workspace: %w", err)
		}
		mediaDir := filepath.Join(ws, "media")
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			return nil, fmt.Errorf("create media dir: %w", err)
		}
		ts := time.Now().Format("20060102-150405")
		basePath = filepath.Join(mediaDir, fmt.Sprintf("img-%s.%s", ts, ext))
	}

	if dir := filepath.Dir(basePath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create output dir: %w", err)
		}
	}

	outputs := make([]string, 0, len(datas))
	for i, raw := range datas {
		path := basePath
		if len(datas) > 1 {
			extDot := filepath.Ext(basePath)
			stem := strings.TrimSuffix(basePath, extDot)
			path = fmt.Sprintf("%s-%d%s", stem, i, extDot)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		outputs = append(outputs, path)
	}
	return outputs, nil
}

func truncateGenImg(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
