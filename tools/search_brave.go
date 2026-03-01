package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// BraveSearchProvider searches via the Brave Search API.
type BraveSearchProvider struct {
	// KeyFn returns the API key at call time (supports runtime config changes).
	KeyFn func() string
}

func (p *BraveSearchProvider) Name() string { return "brave" }

func (p *BraveSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	key := p.KeyFn()
	if key == "" {
		return nil, fmt.Errorf("Brave Search API key not configured. Use the manage-search skill to set it up")
	}

	endpoint := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), maxResults)

	client := &http.Client{Timeout: webSearchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("Brave API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return parseBraveResults(body, maxResults)
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
