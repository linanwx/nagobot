package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/linanwx/nagobot/provider"
)

const (
	webSearchDefaultMaxResults = 5
	webSearchHTTPTimeout       = 15 * time.Second
	webFetchHTTPTimeout        = 30 * time.Second
	webFetchMaxReadBytes       = 500000
	webFetchMaxContentChars    = 10000
)

// WebSearchTool searches the web using DuckDuckGo.
type WebSearchTool struct {
	defaultMaxResults int
}

// Def returns the tool definition.
func (t *WebSearchTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_search",
			Description: "Search the web using DuckDuckGo and return results. Use for finding current information, documentation, etc.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results to return. Defaults to 5.",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

// webSearchArgs are the arguments for web_search.
type webSearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

// Run executes the tool.
func (t *WebSearchTool) Run(ctx context.Context, args json.RawMessage) string {
	var a webSearchArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if a.MaxResults <= 0 {
		if t.defaultMaxResults > 0 {
			a.MaxResults = t.defaultMaxResults
		} else {
			a.MaxResults = webSearchDefaultMaxResults
		}
	}

	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(a.Query))

	client := &http.Client{Timeout: webSearchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return fmt.Sprintf("Error: failed to create request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error: search request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("Error: failed to read response: %v", err)
	}

	results := parseDuckDuckGoResults(string(body), a.MaxResults)
	if len(results) == 0 {
		return "No search results found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", a.Query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}

	return sb.String()
}

// searchResult represents a single search result.
type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// parseDuckDuckGoResults extracts results from DuckDuckGo HTML.
func parseDuckDuckGoResults(html string, maxResults int) []searchResult {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}

	results := make([]searchResult, 0, maxResults)
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
		resolvedURL := normalizeSearchResultURL(rawURL)
		snippet := strings.TrimSpace(sel.Find(".result__snippet").First().Text())

		if title != "" && resolvedURL != "" {
			results = append(results, searchResult{Title: title, URL: resolvedURL, Snippet: snippet})
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
			resolvedURL := normalizeSearchResultURL(rawURL)
			if title != "" && resolvedURL != "" {
				results = append(results, searchResult{Title: title, URL: resolvedURL})
			}
			return len(results) < maxResults
		})
	}

	return results
}

func normalizeSearchResultURL(rawURL string) string {
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

// WebFetchTool fetches content from a URL.
type WebFetchTool struct{}

// Def returns the tool definition.
func (t *WebFetchTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_fetch",
			Description: "Fetch the content of a web page. Returns the text content (HTML tags stripped for readability).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to fetch.",
					},
					"raw": map[string]any{
						"type":        "boolean",
						"description": "If true, return raw HTML instead of stripped text. Defaults to false.",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

// webFetchArgs are the arguments for web_fetch.
type webFetchArgs struct {
	URL string `json:"url"`
	Raw bool   `json:"raw,omitempty"`
}

// Run executes the tool.
func (t *WebFetchTool) Run(ctx context.Context, args json.RawMessage) string {
	var a webFetchArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	parsedURL, err := url.Parse(a.URL)
	if err != nil {
		return fmt.Sprintf("Error: invalid URL: %v", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "Error: only http and https URLs are supported"
	}

	client := &http.Client{Timeout: webFetchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		return fmt.Sprintf("Error: failed to create request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error: request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Error: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxReadBytes))
	if err != nil {
		return fmt.Sprintf("Error: failed to read response: %v", err)
	}

	content := string(body)
	if !a.Raw {
		content = extractTextContent(content)
	}

	if len(content) > webFetchMaxContentChars {
		content, _ = truncateWithNotice(content, webFetchMaxContentChars)
	}

	return content
}

// extractTextContent extracts readable text from HTML.
func extractTextContent(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return strings.TrimSpace(html)
	}

	doc.Find("script,style,noscript").Each(func(_ int, s *goquery.Selection) {
		s.Remove()
	})

	text := strings.TrimSpace(doc.Find("body").Text())
	if text == "" {
		text = strings.TrimSpace(doc.Text())
	}

	lines := strings.Split(text, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Join(strings.Fields(line), " ")
		cleanLines = append(cleanLines, line)
	}

	return strings.Join(cleanLines, "\n")
}
