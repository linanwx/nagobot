package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// DuckDuckGoProvider searches via DuckDuckGo HTML scraping (no API key needed).
type DuckDuckGoProvider struct{}

func (p *DuckDuckGoProvider) Name() string      { return "duckduckgo" }
func (p *DuckDuckGoProvider) Available() bool { return true }

func (p *DuckDuckGoProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	client := &http.Client{Timeout: webSearchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return parseDDGResults(string(body), maxResults), nil
}

// parseDDGResults extracts results from DuckDuckGo HTML.
func parseDDGResults(html string, maxResults int) []SearchResult {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}

	results := make([]SearchResult, 0, maxResults)
	doc.Find("div.result").EachWithBreak(func(_ int, sel *goquery.Selection) bool {
		link := sel.Find("a.result__a").First()
		if link.Length() == 0 {
			return true
		}

		title := strings.TrimSpace(link.Text())
		rawURL, ok := link.Attr("href")
		if !ok {
			return true
		}
		resolvedURL := normalizeDDGURL(rawURL)
		snippet := strings.TrimSpace(sel.Find(".result__snippet").First().Text())

		if title != "" && resolvedURL != "" {
			results = append(results, SearchResult{Title: title, URL: resolvedURL, Snippet: snippet})
		}
		return len(results) < maxResults
	})

	if len(results) == 0 {
		doc.Find("a.result__a").EachWithBreak(func(_ int, link *goquery.Selection) bool {
			title := strings.TrimSpace(link.Text())
			rawURL, ok := link.Attr("href")
			if !ok {
				return true
			}
			resolvedURL := normalizeDDGURL(rawURL)
			if title != "" && resolvedURL != "" {
				results = append(results, SearchResult{Title: title, URL: resolvedURL})
			}
			return len(results) < maxResults
		})
	}

	return results
}

func normalizeDDGURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(rawURL)
	if err != nil {
		decoded = rawURL
	}
	if idx := strings.Index(decoded, "uddg="); idx != -1 {
		u := decoded[idx+5:]
		if ampIdx := strings.Index(u, "&"); ampIdx != -1 {
			u = u[:ampIdx]
		}
		return u
	}
	return rawURL
}
