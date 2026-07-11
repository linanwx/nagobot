// Package embedding provides a client for the embedding endpoint of a locally
// running Ollama instance. The capability is strictly optional: detection runs
// at call time (never at startup), the probe result is cached briefly, and
// callers are expected to degrade gracefully when no Ollama — or no suitable
// model — is present.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// preferredModels ranks the embedding models known to be multilingual, best
// first. Detection picks the first installed match. nomic-embed-text comes
// last: it is English-centric, but still beats no classifier at all.
var preferredModels = []string{
	"qwen3-embedding",
	"bge-m3",
	"snowflake-arctic-embed2",
	"granite-embedding",
	"paraphrase-multilingual",
	"mxbai-embed-large",
	"nomic-embed-text",
}

const detectTTL = time.Minute

// Client talks to one Ollama base URL. The zero value is not usable; construct
// with NewLocal.
type Client struct {
	baseURL string
	hc      *http.Client

	mu        sync.Mutex
	model     string // "" while undetected or unavailable
	checkedAt time.Time
}

// NewLocal returns a client for the local Ollama instance. OLLAMA_HOST is
// honored in both its "host:port" and full-URL forms; the default is
// http://127.0.0.1:11434.
func NewLocal() *Client {
	base := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if base == "" {
		base = "http://127.0.0.1:11434"
	} else if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return &Client{
		baseURL: strings.TrimRight(base, "/"),
		// Generous cap: the first embed after an idle period loads the model
		// into memory. Per-call deadlines come from the caller's ctx.
		hc: &http.Client{Timeout: 60 * time.Second},
	}
}

// Model returns the best installed embedding model, or ok=false when Ollama is
// unreachable or has none we trust. The probe (GET /api/tags) is cached for a
// minute so per-message callers don't hammer it; a failed probe is cached too,
// so a machine without Ollama pays one cheap connection refusal per minute.
func (c *Client) Model(ctx context.Context) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checkedAt) < detectTTL {
		return c.model, c.model != ""
	}
	c.checkedAt = time.Now()
	c.model = c.detect(ctx)
	return c.model, c.model != ""
}

func (c *Client) detect(ctx context.Context) string {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return ""
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "" // no local Ollama — the feature is simply off
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return ""
	}

	installed := make(map[string]string, len(tags.Models)) // base name → full tag
	for _, m := range tags.Models {
		base, _, _ := strings.Cut(m.Name, ":")
		if _, dup := installed[base]; !dup {
			installed[base] = m.Name
		}
	}
	for _, want := range preferredModels {
		if full, ok := installed[want]; ok {
			return full
		}
	}
	return ""
}

// Embed returns one vector per input text, in order, using the detected model.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	model, ok := c.Model(ctx)
	if !ok {
		return nil, fmt.Errorf("embedding: no usable local ollama embedding model")
	}

	body, err := json.Marshal(map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("embedding: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedding: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding: call /api/embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding: /api/embed returned %s", resp.Status)
	}

	var out struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embedding: decode response: %w", err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding: got %d vectors for %d inputs", len(out.Embeddings), len(texts))
	}
	return out.Embeddings, nil
}
