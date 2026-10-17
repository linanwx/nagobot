package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const deepSeekToolsURL = "https://api.deepseek.com/v1/tools/"
const deepSeekToolsMaxResponse = 8 << 20

// Dedicated tool API transport: deliberately independent of chat providers and
// their configurable API bases. Never forward credentials through redirects.
func deepSeekToolsRequest(ctx context.Context, keyFn func() string, operation string, payload, result any, timeout time.Duration, transport http.RoundTripper) error {
	key := ""
	if keyFn != nil {
		key = strings.TrimSpace(keyFn())
	}
	if key == "" {
		return fmt.Errorf("deepseek %s: official DeepSeek API key not configured", operation)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekToolsURL+operation, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("deepseek %s: %w", operation, err)
		}
		limit := int64(deepSeekToolsMaxResponse)
		if resp.StatusCode != http.StatusOK {
			limit = 4096
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			statusErr := fmt.Errorf("deepseek %s: %w", operation, &HTTPError{StatusCode: resp.StatusCode, Status: http.StatusText(resp.StatusCode)})
			retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
			if !retry || attempt == 2 {
				return statusErr
			}
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if readErr != nil {
			return fmt.Errorf("deepseek %s: reading response: %w", operation, readErr)
		}
		if int64(len(data)) > limit {
			return fmt.Errorf("deepseek %s: response exceeds %d bytes", operation, limit)
		}
		if err := json.Unmarshal(data, result); err != nil {
			return fmt.Errorf("deepseek %s: invalid JSON response: %w", operation, err)
		}
		return nil
	}
	return fmt.Errorf("deepseek %s: retry limit reached", operation)
}
