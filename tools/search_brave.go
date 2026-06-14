package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BraveSearchProvider searches via the Brave Search API.
//
// The configured key may be a comma-separated POOL of subscription tokens
// (e.g. several free/Base accounts). Requests are spread evenly across the pool
// with round-robin so each key stays within its own monthly free quota/credit
// and the per-key 1 req/s rate limit is multiplied by the pool size. A key that
// returns 429 / quota / billing-limit is put on a short cooldown and skipped.
type BraveSearchProvider struct {
	// KeyFn returns the API key (or comma-separated key pool) at call time
	// (supports runtime config changes).
	KeyFn func() string
}

func (p *BraveSearchProvider) Name() string { return "brave" }
func (p *BraveSearchProvider) Tags() []string {
	return []string{"paid", "$5/1k queries", "$5/mo free credit", "key-pool round-robin"}
}
func (p *BraveSearchProvider) Available() bool {
	return p.KeyFn != nil && len(parseBraveKeys(p.KeyFn())) > 0
}

// braveCooldownDur is how long a key is skipped after a 429 / quota / billing
// error. Long enough that a rate-limited key recovers and a monthly-exhausted
// key is re-probed at most once per minute (not once per search).
const braveCooldownDur = 60 * time.Second

var (
	braveRot   atomic.Uint64            // round-robin rotation counter (process-global)
	braveCDMu  sync.Mutex               // guards braveCD
	braveCD    = map[string]time.Time{} // key → cooldown-until
	braveNowFn = time.Now               // overridable in tests
)

// parseBraveKeys splits a comma/newline-separated key pool into trimmed,
// de-duplicated, non-empty keys, preserving order.
func parseBraveKeys(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	seen := make(map[string]bool, len(fields))
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		k := strings.TrimSpace(f)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	return keys
}

// braveOrderedKeys returns keys rotated by the global counter so consecutive
// searches start at the next key (even round-robin distribution). Advances the
// counter once per call.
func braveOrderedKeys(keys []string) []string {
	n := len(keys)
	if n <= 1 {
		return keys
	}
	off := int(braveRot.Add(1) - 1)
	out := make([]string, n)
	for i := range keys {
		out[i] = keys[((off+i)%n+n)%n]
	}
	return out
}

func braveOnCooldown(key string) bool {
	braveCDMu.Lock()
	defer braveCDMu.Unlock()
	until, ok := braveCD[key]
	if !ok {
		return false
	}
	if braveNowFn().Before(until) {
		return true
	}
	delete(braveCD, key)
	return false
}

func braveMarkCooldown(key string) {
	braveCDMu.Lock()
	braveCD[key] = braveNowFn().Add(braveCooldownDur)
	braveCDMu.Unlock()
}

func (p *BraveSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if p.KeyFn == nil {
		return nil, fmt.Errorf("Brave Search API key not configured. Use the manage-config skill to set it up")
	}
	keys := parseBraveKeys(p.KeyFn())
	if len(keys) == 0 {
		return nil, fmt.Errorf("Brave Search API key not configured. Use the manage-config skill to set it up")
	}

	ordered := braveOrderedKeys(keys)

	// First pass: live keys (not on cooldown), in round-robin order.
	var lastErr error
	triedLive := false
	for _, key := range ordered {
		if braveOnCooldown(key) {
			continue
		}
		triedLive = true
		results, status, err := braveRequest(ctx, key, query, maxResults)
		if err == nil {
			return results, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
		if braveExhausted(status) {
			braveMarkCooldown(key)
		}
	}

	// Single-key pool: preserve the original 2s-retry-on-429 behavior (free
	// plan 1 req/s; LLM fires parallel searches in one turn). No sibling key
	// to fall over to, so waiting is the only option.
	if len(keys) == 1 && braveExhausted(lastStatusOf(lastErr)) {
		t := time.NewTimer(2 * time.Second)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
		if results, _, err := braveRequest(ctx, keys[0], query, maxResults); err == nil {
			return results, nil
		} else {
			lastErr = err
		}
	}

	// All keys were on cooldown → best-effort one probe each (cooldowns may be
	// stale), still round-robin order.
	if !triedLive {
		for _, key := range ordered {
			results, _, err := braveRequest(ctx, key, query, maxResults)
			if err == nil {
				return results, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("Brave Search: all keys unavailable")
	}
	return nil, lastErr
}

// braveExhausted reports whether an HTTP status means "this key cannot serve
// now" (rate-limited, out of quota, or hit its billing limit) → cooldown it.
func braveExhausted(status int) bool {
	return status == http.StatusTooManyRequests || // 429 rate / quota
		status == http.StatusPaymentRequired || // 402 billing limit
		status == http.StatusForbidden // 403 limit enforced / key disabled
}

// braveStatusErr carries the HTTP status alongside the error so the caller can
// decide whether to cooldown the key.
type braveStatusErr struct {
	status int
	msg    string
}

func (e *braveStatusErr) Error() string { return e.msg }

func lastStatusOf(err error) int {
	if se, ok := err.(*braveStatusErr); ok {
		return se.status
	}
	return 0
}

// braveRequest performs a single Brave web-search call with one key. Returns
// (results, httpStatus, err). On non-200 the error is a *braveStatusErr.
func braveRequest(ctx context.Context, key, query string, maxResults int) ([]SearchResult, int, error) {
	endpoint := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)

	client := &http.Client{Timeout: webSearchHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, resp.StatusCode, &braveStatusErr{
			status: resp.StatusCode,
			msg:    fmt.Sprintf("Brave API error: HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}
	results, err := parseBraveResults(body, maxResults)
	return results, resp.StatusCode, err
}

// braveResponse is the top-level Brave Search API response.
type braveResponse struct {
	Web struct {
		Results []braveWebResult `json:"results"`
	} `json:"web"`
}

type braveWebResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func parseBraveResults(data []byte, maxResults int) ([]SearchResult, error) {
	var resp braveResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse Brave response: %w", err)
	}

	results := make([]SearchResult, 0, maxResults)
	for _, r := range resp.Web.Results {
		if len(results) >= maxResults {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}
	return results, nil
}
