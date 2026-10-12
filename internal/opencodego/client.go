package opencodego

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultAPIBase = "https://opencode.ai/zen/go/v1"

type Client struct {
	apiKey, apiBase, fallbackSession string
	http                             *http.Client
}

func NewClient(apiKey, apiBase string) *Client {
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	for _, suffix := range []string{"/chat/completions", "/messages"} {
		apiBase = strings.TrimSuffix(apiBase, suffix)
	}
	var random [16]byte
	_, _ = rand.Read(random[:])
	return &Client{apiKey: apiKey, apiBase: apiBase, fallbackSession: hex.EncodeToString(random[:]), http: &http.Client{
		// Keep credentials and all inference requests on the configured gateway.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body) }

func (c *Client) Chat(ctx context.Context, sessionID string, req Request, emit func(Delta)) (*Response, error) {
	model, ok := ModelInfo(req.Model)
	if !ok {
		return nil, fmt.Errorf("unsupported OpenCode Go model %q", req.Model)
	}
	for _, message := range req.Messages {
		if !model.Vision && len(message.Images) > 0 {
			return nil, fmt.Errorf("OpenCode Go model %s does not support images", req.Model)
		}
	}
	if req.Effort != "" {
		valid := false
		for _, effort := range model.Efforts {
			valid = valid || effort == req.Effort
		}
		if !valid {
			return nil, fmt.Errorf("unsupported OpenCode Go effort %q for %s", req.Effort, req.Model)
		}
	}
	if sessionID == "" {
		sessionID = c.fallbackSession
	}
	if emit == nil {
		emit = func(Delta) {}
	}
	if req.Model == "claude-haiku-5-5" {
		body, err := messagesRequest(req)
		if err != nil {
			return nil, err
		}
		resp, err := c.post(ctx, sessionID, "/messages", body)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return readMessages(resp.Body, emit)
	}
	body, err := chatRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.post(ctx, sessionID, "/chat/completions", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readChat(resp.Body, emit)
}

func (c *Client) post(ctx context.Context, sessionID, endpoint string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode OpenCode Go request: %w", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("User-Agent", "nagobot/1.0")
		req.Header.Set("x-opencode-session", sessionID)
		if endpoint == "/messages" {
			req.Header.Set("x-api-key", c.apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
			req.Header.Set("anthropic-beta", "thinking-binding-controls-2026-08-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		resp, err := c.http.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		retry := err != nil
		if resp != nil {
			content, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if readErr != nil && err == nil {
				err = readErr
			}
			if err == nil {
				err = &HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(content))}
			}
			retry = resp.StatusCode == 408 || resp.StatusCode == 409 || resp.StatusCode == 429 || resp.StatusCode >= 500
		}
		if !retry || attempt == 2 || ctx.Err() != nil {
			return nil, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable")
}
