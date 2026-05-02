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
	Short:   "Generate an image with OpenAI gpt-image-2 and save it to disk",
	GroupID: "internal",
	RunE:    runGenerateImage,
}

func init() {
	generateImageCmd.Flags().StringVarP(&genImagePrompt, "prompt", "p", "", "Image prompt (required)")
	generateImageCmd.Flags().StringVar(&genImageModel, "model", "gpt-image-2", "OpenAI image model")
	generateImageCmd.Flags().StringVar(&genImageSize, "size", "auto", "auto | 1024x1024 | 1536x1024 | 1024x1536")
	generateImageCmd.Flags().StringVar(&genImageQuality, "quality", "auto", "auto | low | medium | high")
	generateImageCmd.Flags().StringVar(&genImageFormat, "format", "png", "png | jpeg (gpt-image-2 ignores webp and returns png)")
	generateImageCmd.Flags().IntVar(&genImageCompression, "compression", -1, "Output compression 0-100 (jpeg only; -1 = omit)")
	generateImageCmd.Flags().IntVarP(&genImageN, "n", "n", 1, "Number of images to generate (1-10)")
	generateImageCmd.Flags().StringVarP(&genImageOutput, "output", "o", "", "Output file path. Default: {workspace}/media/img-{ts}.{ext}; with n>1 the index is appended.")
	_ = generateImageCmd.MarkFlagRequired("prompt")
	rootCmd.AddCommand(generateImageCmd)
}

type genImageRequest struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	N                 int    `json:"n,omitempty"`
	Size              string `json:"size,omitempty"`
	Quality           string `json:"quality,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
}

type genImageResponse struct {
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
	pc := cfg.Providers.OpenAI
	if pc == nil || pc.APIKey == "" {
		return fmt.Errorf("openai apiKey not configured (run `nagobot set-provider-key openai`)")
	}

	endpoint := openaiImagesEndpointDefault
	if base := strings.TrimRight(pc.APIBase, "/"); base != "" && base != openaiImagesAPIBaseDefault {
		endpoint = base + "/v1/images/generations"
	}

	req := genImageRequest{
		Model:  genImageModel,
		Prompt: prompt,
		N:      genImageN,
	}
	if s := strings.TrimSpace(genImageSize); s != "" && s != "auto" {
		req.Size = s
	}
	if q := strings.TrimSpace(genImageQuality); q != "" && q != "auto" {
		req.Quality = q
	}
	if format == "jpeg" {
		req.OutputFormat = "jpeg"
	} else {
		req.OutputFormat = "png"
	}
	if format == "jpeg" && genImageCompression >= 0 {
		if genImageCompression > 100 {
			return fmt.Errorf("--compression must be 0-100")
		}
		c := genImageCompression
		req.OutputCompression = &c
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+pc.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<27)) // 128 MiB cap
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var parsed genImageResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return fmt.Errorf("parse response (HTTP %d): %w; body: %s",
			resp.StatusCode, err, truncateGenImg(string(respBytes), 400))
	}
	if resp.StatusCode != http.StatusOK {
		msg := "unknown error"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, msg)
	}
	if len(parsed.Data) == 0 {
		return fmt.Errorf("openai returned no images: %s", truncateGenImg(string(respBytes), 400))
	}

	outputs, err := writeImages(cfg, parsed.Data, format)
	if err != nil {
		return err
	}

	pairs := [][2]string{
		{"command", "generate-image"},
		{"status", "ok"},
		{"model", genImageModel},
		{"count", fmt.Sprintf("%d", len(outputs))},
		{"input_tokens", fmt.Sprintf("%d", parsed.Usage.InputTokens)},
		{"output_tokens", fmt.Sprintf("%d", parsed.Usage.OutputTokens)},
	}
	if req.Size != "" {
		pairs = append(pairs, [2]string{"size", req.Size})
	}
	if req.Quality != "" {
		pairs = append(pairs, [2]string{"quality", req.Quality})
	}
	pairs = append(pairs, [2]string{"format", format})
	for i, p := range outputs {
		pairs = append(pairs, [2]string{fmt.Sprintf("path_%d", i), p})
	}

	body0 := strings.Join(outputs, "\n")
	fmt.Print(tools.CmdOutput(pairs, body0) + "\n")
	return nil
}

func writeImages(cfg *config.Config, data []struct {
	B64JSON string `json:"b64_json"`
}, format string) ([]string, error) {
	ext := format
	if ext == "jpeg" {
		ext = "jpg"
	}

	// Resolve base path. If user supplied --output we honor it (and append
	// -0/-1/... for n>1). Otherwise default to {workspace}/media/img-{ts}.ext
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

	outputs := make([]string, 0, len(data))
	for i, d := range data {
		if d.B64JSON == "" {
			return nil, fmt.Errorf("data[%d].b64_json is empty", i)
		}
		raw, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("decode b64 for data[%d]: %w", i, err)
		}
		path := basePath
		if len(data) > 1 {
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
