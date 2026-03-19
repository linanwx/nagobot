package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OpenSearchProvider searches via the Alibaba Cloud OpenSearch AI Search Platform API.
// Host is account-specific, obtained from the console at:
// https://opensearch.console.aliyun.com/cn-shanghai/rag/api-key
// Format: {workspace}-{id}.platform-cn-shanghai.opensearch.aliyuncs.com
type OpenSearchProvider struct {
	// KeyFn returns the API key at call time (supports runtime config changes).
	KeyFn func() string
	// HostFn returns the API host at call time (account-specific, from console).
	HostFn func() string
}

func (p *OpenSearchProvider) Name() string { return "opensearch" }
func (p *OpenSearchProvider) Available() bool {
	return p.KeyFn != nil && p.KeyFn() != "" && p.HostFn != nil && p.HostFn() != ""
}

func (p *OpenSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	key := p.KeyFn()
	if key == "" {
		return nil, fmt.Errorf("OpenSearch API key not configured. Use the manage-config skill to set it up")
	}
	host := p.HostFn()
	if host == "" {
		return nil, fmt.Errorf("OpenSearch API host not configured. Use the manage-config skill to set it up")
	}

	if maxResults > 50 {
		maxResults = 50
	}

	// Extract workspace from host (format: {workspace}-{id}.platform-...)
	// Default to "default" if parsing fails.
	workspace := "default"
	if dashIdx := strings.Index(host, "-"); dashIdx > 0 {
		workspace = host[:dashIdx]
	}

	endpoint := fmt.Sprintf("https://%s/v3/openapi/workspaces/%s/web-search/ops-web-search-001", host, workspace)

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

	body, err := readSearchResponse(resp, "OpenSearch")
	if err != nil {
		return nil, err
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
	Title    string `json:"title"`
	Link     string `json:"link"`
	Snippet  string `json:"snippet"`
	Content  string `json:"content"`
	MetaInfo struct {
		PublishedTime string `json:"publishedTime"`
	} `json:"meta_info"`
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
			PublishDate: r.MetaInfo.PublishedTime,
		})
	}
	return results, nil
}
