package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OpenSearchProvider searches via the Alibaba Cloud OpenSearch Web Search API.
type OpenSearchProvider struct {
	// KeyFn returns the API key at call time (supports runtime config changes).
	KeyFn func() string
	// WorkspaceFn returns the workspace ID at call time.
	WorkspaceFn func() string
}

func (p *OpenSearchProvider) Name() string { return "opensearch" }
func (p *OpenSearchProvider) Available() bool {
	return p.KeyFn != nil && p.KeyFn() != "" && p.WorkspaceFn != nil && p.WorkspaceFn() != ""
}

func (p *OpenSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	key := p.KeyFn()
	if key == "" {
		return nil, fmt.Errorf("OpenSearch API key not configured. Use the manage-search skill to set it up")
	}
	workspace := p.WorkspaceFn()
	if workspace == "" {
		return nil, fmt.Errorf("OpenSearch workspace ID not configured. Use the manage-search skill to set it up")
	}

	if maxResults > 50 {
		maxResults = 50
	}

	endpoint := fmt.Sprintf(
		"https://opensearch.cn-shanghai.aliyuncs.com/v3/openapi/workspaces/%s/web-search/ops-web-search-001",
		workspace,
	)

	reqBody := openSearchRequest{
		Query:        query,
		TopK:         maxResults,
		QueryRewrite: false,
		ContentType:  "snippet",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: webSearchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
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
		return nil, fmt.Errorf("OpenSearch API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return parseOpenSearchResults(body, maxResults)
}

type openSearchRequest struct {
	Query        string `json:"query"`
	TopK         int    `json:"top_k"`
	QueryRewrite bool   `json:"query_rewrite"`
	ContentType  string `json:"content_type"`
}

type openSearchResponse struct {
	Result struct {
		SearchResult []openSearchResult `json:"search_result"`
	} `json:"result"`
}

type openSearchResult struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Snippet     string `json:"snippet"`
	PublishDate string `json:"publish_date"`
	Source      string `json:"source"`
}

func parseOpenSearchResults(data []byte, maxResults int) ([]SearchResult, error) {
	var resp openSearchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse OpenSearch response: %w", err)
	}

	results := make([]SearchResult, 0, maxResults)
	for _, r := range resp.Result.SearchResult {
		if len(results) >= maxResults {
			break
		}
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.Link,
			Snippet:     r.Snippet,
			PublishDate: r.PublishDate,
			Source:      r.Source,
		})
	}
	return results, nil
}
