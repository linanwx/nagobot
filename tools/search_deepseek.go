package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// DeepSeekSearchProvider uses the official standalone Search API, not an LLM.
type DeepSeekSearchProvider struct {
	KeyFn     func() string
	transport http.RoundTripper
}

func (p *DeepSeekSearchProvider) Name() string { return "deepseek" }
func (p *DeepSeekSearchProvider) Tags() []string {
	return []string{"paid", "¥5/1k successful nonempty requests", "official DeepSeek key", "5 QPS/account"}
}
func (p *DeepSeekSearchProvider) Available() bool {
	return p.KeyFn != nil && strings.TrimSpace(p.KeyFn()) != ""
}
func (p *DeepSeekSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" || !utf8.ValidString(query) || utf8.RuneCountInString(query) > 128 {
		return nil, fmt.Errorf("deepseek search: query must contain 1–128 Unicode characters and cannot be blank")
	}
	if maxResults < 1 || maxResults > 50 {
		return nil, fmt.Errorf("deepseek search: max_results must be between 1 and 50")
	}
	request := struct {
		Query       string `json:"query"`
		Count       int    `json:"count"`
		NeedContent bool   `json:"need_content"`
	}{query, maxResults, false}
	var response struct {
		Results *[]struct {
			Title    string `json:"title"`
			URL      string `json:"url"`
			Snippet  string `json:"snippet"`
			Summary  string `json:"summary"`
			SiteName string `json:"site_name"`
			Date     string `json:"date"`
		} `json:"results"`
	}
	if err := deepSeekToolsRequest(ctx, p.KeyFn, "search", request, &response, webSearchHTTPTimeout, p.transport); err != nil {
		return nil, err
	}
	if response.Results == nil {
		return nil, fmt.Errorf("deepseek search: response missing results array")
	}
	results := make([]SearchResult, 0, len(*response.Results))
	for _, r := range *response.Results {
		if len(results) >= maxResults {
			break
		}
		snippet := r.Summary
		if strings.TrimSpace(snippet) == "" {
			snippet = r.Snippet
		}
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: snippet, Source: r.SiteName, PublishDate: r.Date})
	}
	return results, nil
}
