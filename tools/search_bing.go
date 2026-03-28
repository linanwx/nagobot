package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// bingSearcher is the shared implementation for Bing search variants.
type bingSearcher struct {
	host string // "cn.bing.com" or "www.bing.com"
}

func (b *bingSearcher) search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	searchURL := fmt.Sprintf("https://%s/search?q=%s&count=%d", b.host, url.QueryEscape(query), maxResults)

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
				URL:     decodeBingURL(href),
				Snippet: snippet,
			})
		}
		return len(results) < maxResults
	})

	return results
}

// decodeBingURL extracts the real URL from Bing's tracking redirect.
// www.bing.com wraps URLs as /ck/a?...&u=a1<base64url>...
// cn.bing.com returns direct URLs, so this is a no-op for them.
func decodeBingURL(href string) string {
	parsed, err := url.Parse(href)
	if err != nil {
		return href
	}
	u := parsed.Query().Get("u")
	if u == "" {
		return href
	}
	// Strip the "a1" prefix that Bing adds before the base64 payload.
	if strings.HasPrefix(u, "a1") {
		u = u[2:]
	}
	decoded, err := base64.RawURLEncoding.DecodeString(u)
	if err != nil {
		return href
	}
	return string(decoded)
}

// BingCNProvider searches via cn.bing.com (no API key needed).
// Best for mainland China where other search engines are blocked.
type BingCNProvider struct{ bingSearcher }

func NewBingCNProvider() *BingCNProvider {
	return &BingCNProvider{bingSearcher{host: "cn.bing.com"}}
}

func (p *BingCNProvider) Name() string      { return "bing-cn" }
func (p *BingCNProvider) Available() bool   { return true }
func (p *BingCNProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	return p.search(ctx, query, maxResults)
}

// BingProvider searches via www.bing.com (no API key needed).
// International Bing — works outside China.
type BingProvider struct{ bingSearcher }

func NewBingProvider() *BingProvider {
	return &BingProvider{bingSearcher{host: "www.bing.com"}}
}

func (p *BingProvider) Name() string      { return "bing" }
func (p *BingProvider) Available() bool   { return true }
func (p *BingProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	return p.search(ctx, query, maxResults)
}
