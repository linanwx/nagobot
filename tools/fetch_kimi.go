package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// KimiFetchProvider fetches pages via the Moonshot/Kimi Formula API (moonshot/fetch).
// Returns clean text content. Currently free (limited-time).
type KimiFetchProvider struct {
	KeyFn        func() string
	BaseURL      string   // "https://api.moonshot.cn" or "https://api.moonshot.ai"
	ProviderTags []string
}

var kimiFetchClient = &http.Client{Timeout: webFetchHTTPTimeout}

func (p *KimiFetchProvider) Name() string            { return "kimi" }
func (p *KimiFetchProvider) Tags() []string          { return p.ProviderTags }
func (p *KimiFetchProvider) Available() bool         { return p.KeyFn != nil && p.KeyFn() != "" }
func (p *KimiFetchProvider) ReturnsMarkdown() bool   { return true }

// kimiFetchRequest is the wire format for the Kimi Formula API.
type kimiFetchRequest struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (p *KimiFetchProvider) Fetch(ctx context.Context, rawURL string) (string, error) {
	endpoint := strings.TrimRight(p.BaseURL, "/") + "/v1/formulas/moonshot/fetch:latest/fibers"

	argsJSON, _ := json.Marshal(map[string]string{"url": rawURL})
	bodyBytes, _ := json.Marshal(kimiFetchRequest{Name: "fetch", Arguments: string(argsJSON)})

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := p.KeyFn(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := kimiFetchClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kimi fetch: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxReadBytes))
	if err != nil {
		return "", err
	}

	var result struct {
		Status  string `json:"status"`
		Context struct {
			Output string `json:"output"`
		} `json:"context"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("kimi fetch: failed to parse response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("kimi fetch: %s", result.Error.Message)
	}
	if result.Status != "succeeded" {
		return "", fmt.Errorf("kimi fetch: status %s", result.Status)
	}

	return result.Context.Output, nil
}
