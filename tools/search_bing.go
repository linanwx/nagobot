package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// BingProvider searches via Bing CN HTML scraping (no API key needed).
// Works in regions where DuckDuckGo/Brave are blocked (e.g. mainland China).
type BingProvider struct{}

func (p *BingProvider) Name() string    { return "bing-cn" }
func (p *BingProvider) Available() bool { return true }

func (p *BingProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	searchURL := fmt.Sprintf("https://cn.bing.com/search?q=%s&count=%d", url.QueryEscape(query), maxResults)

	client := &http.Client{Timeout: webSearchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return parseBingResults(doc, maxResults), nil
}

// parseBingResults extracts search results from Bing HTML.
func parseBingResults(doc *goquery.Document, maxResults int) []SearchResult {
	results := make([]SearchResult, 0, maxResults)

	doc.Find("li.b_algo").EachWithBreak(func(_ int, sel *goquery.Selection) bool {
		// Title and URL from h2 > a
		link := sel.Find("h2 a").First()
		if link.Length() == 0 {
			return true
		}

		title := strings.TrimSpace(link.Text())
		href, ok := link.Attr("href")
		if !ok || href == "" {
			return true
		}

		// Snippet from p.b_lineclamp2 or div.b_caption p
		snippet := strings.TrimSpace(sel.Find("p.b_lineclamp2").First().Text())
		if snippet == "" {
			snippet = strings.TrimSpace(sel.Find("div.b_caption p").First().Text())
		}

		if title != "" {
			results = append(results, SearchResult{
				Title:   title,
				URL:     href,
				Snippet: snippet,
			})
		}
		return len(results) < maxResults
	})

	return results
}
