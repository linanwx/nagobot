package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
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

// WebSearchTool searches the web using pluggable providers.
type WebSearchTool struct {
	defaultMaxResults int
	providers         map[string]SearchProvider
	healthChecker     *SearchHealthChecker
	Guide             string // injected from WEB_SEARCH_GUIDE.md, appended to error responses
}

// Def returns the tool definition.
func (t *WebSearchTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_search",
			Description: "Search the web.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Max results. Default: 5.",
					},
					"source": map[string]any{
						"type":        "string",
						"description": "Search source. Empty to see guide.",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

// webSearchArgs are the arguments for web_search.
type webSearchArgs struct {
	Query      string `json:"query" required:"true"`
	MaxResults int    `json:"max_results,omitempty"`
	Source     string `json:"source,omitempty"`
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

	source := a.Source
	if source == "" {
		return t.sourceError("source is required")
	}

	p, ok := t.providers[source]
	if !ok {
		return t.sourceError(fmt.Sprintf("unknown search source %q", source))
	}
	if !p.Available() {
		return t.sourceError(fmt.Sprintf("search source %q is not available", source))
	}

	start := time.Now()
	results, err := p.Search(ctx, a.Query, a.MaxResults)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		if t.healthChecker != nil {
			t.healthChecker.Record(source, false, 0, elapsed)
		}
		return t.searchError(source, a.Query, err)
	}

	if t.healthChecker != nil {
		t.healthChecker.Record(source, true, len(results), elapsed)
	}

	if len(results) == 0 {
		return t.emptyResults(source, a.Query)
	}

	sourceTags := ""
	if tags := p.Tags(); len(tags) > 0 {
		sourceTags = " [" + strings.Join(tags, ",") + "]"
	}
	fields := map[string]any{
		"query":   a.Query,
		"source":  source + sourceTags,
		"results": len(results),
	}
	if t.healthChecker != nil {
		fields["source_status"] = t.healthChecker.StatusSummary()
	}
	return toolResult("web_search", fields, FormatSearchResults(a.Query, results))
}

func (t *WebSearchTool) sourceError(msg string) string {
	return buildSourceError(msg, t.healthChecker, t.Guide)
}

func (t *WebSearchTool) searchError(source, query string, err error) string {
	return buildToolError("web_search", fmt.Sprintf("Error: search on %q failed: %v", source, err), t.healthChecker, t.Guide)
}

func (t *WebSearchTool) emptyResults(source, query string) string {
	fields := map[string]any{
		"query":   query,
		"source":  source,
		"results": 0,
	}
	var body strings.Builder
	body.WriteString(fmt.Sprintf("No results found for %q on %s.\n\nTry a different source or rephrase the query.\n", query, source))
	if t.healthChecker != nil {
		body.WriteString(t.healthChecker.DetailedStatus())
	}
	appendGuide(&body, t.Guide)
	return toolResult("web_search", fields, body.String())
}

// webFetchCache is a simple in-memory cache for fetched page content.
var webFetchCache = struct {
	sync.Mutex
	entries map[string]webFetchCacheEntry
}{entries: make(map[string]webFetchCacheEntry)}

type webFetchCacheEntry struct {
	content   string
	fetchedAt time.Time
}

const webFetchCacheTTL = 10 * time.Minute

// WebFetchTool fetches content from a URL using pluggable providers.
type WebFetchTool struct {
	providers     map[string]FetchProvider
	healthChecker *SearchHealthChecker // reused from web_search — tracks fetch outcomes
	Guide         string              // injected from WEB_FETCH_GUIDE.md, appended to error responses
}

// Def returns the tool definition.
func (t *WebFetchTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_fetch",
			Description: "Fetch the content of a web page. Returns readable text/markdown. Content is cached for 10 minutes — repeated fetches of the same URL are free. Use offset/limit to paginate through long pages without re-fetching. If a site returns 403/503 (anti-bot), retry with a different source.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to fetch.",
					},
					"source": map[string]any{
						"type":        "string",
						"description": "Fetch source. Empty to see guide.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Character offset to start returning content from. Use to paginate through long pages. Default: 0.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of characters to return. Default: 10000.",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

// webFetchArgs are the arguments for web_fetch.
type webFetchArgs struct {
	URL    string `json:"url" required:"true"`
	Source string `json:"source,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
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

	source := a.Source
	if source == "" {
		return t.fetchSourceError("source is required")
	}

	p, ok := t.providers[source]
	if !ok {
		return t.fetchSourceError(fmt.Sprintf("unknown fetch source %q", source))
	}
	if !p.Available() {
		return t.fetchSourceError(fmt.Sprintf("fetch source %q is not available", source))
	}

	cacheKey := a.URL + "::" + source

	// Check cache
	content, cached := webFetchCacheLookup(cacheKey)
	if !cached {
		start := time.Now()
		content, err = p.Fetch(ctx, a.URL)
		elapsed := time.Since(start).Milliseconds()

		if err != nil {
			if t.healthChecker != nil {
				t.healthChecker.Record(source, false, 0, elapsed)
			}
			return t.fetchError(source, a.URL, err)
		}

		if t.healthChecker != nil {
			t.healthChecker.Record(source, true, len(content), elapsed)
		}

		// Providers that return raw HTML need content extraction.
		if !p.ReturnsMarkdown() {
			content = extractTextContent(content)
		}

		webFetchCacheStore(cacheKey, content)
	}

	totalChars := len(content)

	// Apply offset/limit pagination
	offset := a.Offset
	limit := a.Limit
	if limit <= 0 {
		limit = webFetchMaxContentChars
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= totalChars {
		return toolError("web_fetch", fmt.Sprintf("offset %d is beyond end of content (total: %d chars)", offset, totalChars))
	}

	end := offset + limit
	if end > totalChars {
		end = totalChars
	}
	slice := content[offset:end]

	sourceTags := ""
	if tags := p.Tags(); len(tags) > 0 {
		sourceTags = " [" + strings.Join(tags, ",") + "]"
	}
	fields := map[string]any{
		"url":         a.URL,
		"source":      source + sourceTags,
		"total_chars": totalChars,
		"showing":     fmt.Sprintf("%d-%d", offset, end),
	}
	if cached {
		fields["cached"] = true
	}
	if end < totalChars {
		fields["next_offset"] = end
	}
	if t.healthChecker != nil {
		fields["source_status"] = t.healthChecker.StatusSummary()
	}

	return toolResult("web_fetch", fields, slice)
}

func (t *WebFetchTool) fetchSourceError(msg string) string {
	return buildSourceError(msg, t.healthChecker, t.Guide)
}

func (t *WebFetchTool) fetchError(source, fetchURL string, err error) string {
	return buildToolError("web_fetch", fmt.Sprintf("Error: fetch %q via %s failed: %v", fetchURL, source, err), t.healthChecker, t.Guide)
}

func webFetchCacheLookup(key string) (string, bool) {
	webFetchCache.Lock()
	defer webFetchCache.Unlock()
	entry, ok := webFetchCache.entries[key]
	if !ok || time.Since(entry.fetchedAt) > webFetchCacheTTL {
		if ok {
			delete(webFetchCache.entries, key)
		}
		return "", false
	}
	return entry.content, true
}

func webFetchCacheStore(key, content string) {
	webFetchCache.Lock()
	defer webFetchCache.Unlock()
	// Evict expired entries if cache grows beyond 20
	if len(webFetchCache.entries) >= 20 {
		now := time.Now()
		for k, e := range webFetchCache.entries {
			if now.Sub(e.fetchedAt) > webFetchCacheTTL {
				delete(webFetchCache.entries, k)
			}
		}
	}
	webFetchCache.entries[key] = webFetchCacheEntry{content: content, fetchedAt: time.Now()}
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

// buildSourceError builds an error message with health status and guide for source selection failures.
func buildSourceError(msg string, hc *SearchHealthChecker, guide string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Error: %s.\n\n", msg))
	if hc != nil {
		sb.WriteString(hc.DetailedStatus())
	}
	appendGuide(&sb, guide)
	return sb.String()
}

// buildToolError builds a tool error with health status and guide.
func buildToolError(toolName, errMsg string, hc *SearchHealthChecker, guide string) string {
	var sb strings.Builder
	sb.WriteString(errMsg)
	sb.WriteString("\n\nTry a different source.\n")
	if hc != nil {
		sb.WriteString(hc.DetailedStatus())
	}
	appendGuide(&sb, guide)
	return toolError(toolName, sb.String())
}

func appendGuide(sb *strings.Builder, guide string) {
	if guide != "" {
		sb.WriteString("\n")
		sb.WriteString(guide)
		sb.WriteString("\n")
	}
}
