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
	"sort"
	"strings"
	"sync"
	"time"
)

// preferredModels ranks the embedding models known to be multilingual, best
// first. Detection picks the first installed match. nomic-embed-text comes
// last: it is English-centric, but still beats no classifier at all.
//
// An entry carrying a tag ("qwen3-embedding:0.6b") matches only that exact tag;
// a bare family name matches any tag of that family. The first entry is pinned
// on purpose: qwen3-embedding ships in 0.6B, 4B and 8B, all three answer the
// same API and classify about as well, but the numbers this package is built
// around — ~150ms warm, ~640MB resident — are the 0.6B ones. Left unpinned, a
// machine that happens to have the 8B pulled for something else would silently
// load it here and blow the pre-think budget on every message. The bare family
// name still follows as a fallback, so a host with only the 4B keeps working.
var preferredModels = []string{
	"qwen3-embedding:0.6b",
	"qwen3-embedding", // any other size, when 0.6b is not installed
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

	installed := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		if n := strings.TrimSpace(m.Name); n != "" {
			installed = append(installed, n)
		}
	}
	// The order /api/tags returns is not contractual, so a host with several tags
	// of one family used to resolve to whichever came back first — a different
	// model across restarts, from the same installation. Sort, and the fallback
	// arm of preferredModels becomes deterministic.
	sort.Strings(installed)

	for _, want := range preferredModels {
		exact := strings.Contains(want, ":")
		for _, tag := range installed {
			name := tag
			if !exact {
				name, _, _ = strings.Cut(tag, ":")
			}
			if strings.EqualFold(name, want) {
				return tag
			}
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
