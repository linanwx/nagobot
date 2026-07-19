// Package embedding provides a client for OpenAI-compatible /embeddings
// endpoints (SiliconFlow, OpenRouter). The capability is strictly optional:
// which backend to use — or whether one exists at all — is resolved from
// configuration at call time, and callers are expected to degrade gracefully
// when no backend is configured.
//
// This replaces a local-Ollama client. The sidecar was dropped because remote
// embedding costs cents per month while the ~1GB resident Ollama process
// dictated the deployment's machine size; the model moved from
// qwen3-embedding:0.6b to Qwen3-Embedding-4B in the same change (the 4B is the
// best-behaved endpoint measured: SiliconFlow serves it at ~170ms p50 with no
// tail, and with instruction formatting it scores 0 misses / 15∕15 held-out on
// the destructive set, vs 11 misses for the raw-text 0.6b). The 8B is NOT a
// drop-in upgrade: SiliconFlow's 8B serving shows a 36% rate of >2s calls,
// which blows the pre-think budget more often than it answers inside it.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

// Backend is one resolved remote embedding endpoint.
type Backend struct {
	Name   string // provider slug ("siliconflow-cn"); part of the model identity
	URL    string // full endpoint URL for POST, e.g. https://api.siliconflow.cn/v1/embeddings
	APIKey string
	Model  string // model name as the endpoint spells it, e.g. Qwen/Qwen3-Embedding-4B
}

// Client resolves a backend from configuration on every call, so a key added
// or removed via /init takes effect without a restart — the same hot-reload
// contract the provider layer follows. The zero value is not usable; construct
// with NewChain.
type Client struct {
	resolve func() *Backend
	hc      *http.Client
}

// NewChain returns a client that asks resolve for the current backend on each
// call. resolve returning nil means the feature is off — Model reports
// unavailable and callers fall back to their regex paths.
func NewChain(resolve func() *Backend) *Client {
	return &Client{
		resolve: resolve,
		// Generous transport cap; per-call deadlines come from the caller's ctx.
		hc: &http.Client{},
	}
}

// Model returns the identity of the resolved backend+model, or ok=false when
// no backend is configured. The identity includes the provider slug so caches
// keyed on it rebuild when the chain resolves differently (e.g. a SiliconFlow
// key appears and takes precedence over OpenRouter).
func (c *Client) Model(ctx context.Context) (string, bool) {
	b := c.resolve()
	if b == nil {
		return "", false
	}
	return b.Name + "/" + b.Model, true
}

// Embed returns one vector per input text, in order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	b := c.resolve()
	if b == nil {
		return nil, fmt.Errorf("embedding: no remote embedding backend configured")
	}

	body, err := json.Marshal(map[string]any{"model": b.Model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("embedding: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedding: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.APIKey)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding: call %s: %w", b.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding: %s returned %s", b.Name, resp.Status)
	}

	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embedding: decode response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embedding: got %d vectors for %d inputs", len(out.Data), len(texts))
	}
	// The spec says entries carry an index; sort rather than trust arrival order.
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	vecs := make([][]float64, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}
