package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ZhipuSearchProvider searches via the Zhipu AI web search API.
// API docs: https://open.bigmodel.cn/
type ZhipuSearchProvider struct {
	// KeyFn returns the API key at call time (supports runtime config changes).
	KeyFn func() string
}

func (p *ZhipuSearchProvider) Name() string { return "zhipu" }
func (p *ZhipuSearchProvider) Available() bool {
	return p.KeyFn != nil && p.KeyFn() != ""
}

func (p *ZhipuSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	key := p.KeyFn()
	if key == "" {
		return nil, fmt.Errorf("Zhipu API key not configured. Use the manage-config skill to set it up")
	}

	if maxResults > 50 {
		maxResults = 50
	}

	reqBody := zhipuSearchRequest{
		SearchEngine: "search_pro",
		SearchQuery:  query,
		Count:        maxResults,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: webSearchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://open.bigmodel.cn/api/paas/v4/web_search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("Zhipu API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return parseZhipuResults(body, maxResults)
}

type zhipuSearchRequest struct {
	SearchEngine string `json:"search_engine"`
	SearchQuery  string `json:"search_query"`
	Count        int    `json:"count,omitempty"`
}

type zhipuSearchResponse struct {
	SearchResult []zhipuSearchResult `json:"search_result"`
}

type zhipuSearchResult struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Content     string `json:"content"`
	Media       string `json:"media"`
	PublishDate string `json:"publish_date"`
}

func parseZhipuResults(data []byte, maxResults int) ([]SearchResult, error) {
	var resp zhipuSearchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse Zhipu response: %w", err)
	}

	results := make([]SearchResult, 0, maxResults)
	for _, r := range resp.SearchResult {
		if len(results) >= maxResults {
			break
		}
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.Link,
			Snippet:     r.Content,
			PublishDate: r.PublishDate,
			Source:      r.Media,
		})
	}
	return results, nil
}
