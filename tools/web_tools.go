package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
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
	Query      string `json:"query"`
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

// sourceError returns an error with detailed health status when source selection fails.
func (t *WebSearchTool) sourceError(msg string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Error: %s.\n\n", msg))
	if t.healthChecker != nil {
		sb.WriteString(t.healthChecker.DetailedStatus())
	} else {
		available := make([]string, 0, len(t.providers))
		for name, prov := range t.providers {
			if prov.Available() {
				available = append(available, name)
			}
		}
		sort.Strings(available)
		sb.WriteString(fmt.Sprintf("Available sources: %s\n", strings.Join(available, ", ")))
	}
	t.appendGuide(&sb)
	return sb.String()
}

// searchError returns an error with health status when a search fails.
func (t *WebSearchTool) searchError(source, query string, err error) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Error: search on %q failed: %v\n\n", source, err))
	sb.WriteString("Try a different source.\n")
	if t.healthChecker != nil {
		sb.WriteString(t.healthChecker.DetailedStatus())
	}
	t.appendGuide(&sb)
	return toolError("web_search", sb.String())
}

// emptyResults returns a message with health status when no results are found.
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
	t.appendGuide(&body)
	return toolResult("web_search", fields, body.String())
}

// appendGuide appends WEB_SEARCH_GUIDE content if available.
func (t *WebSearchTool) appendGuide(sb *strings.Builder) {
	if t.Guide != "" {
		sb.WriteString("\n")
		sb.WriteString(t.Guide)
		sb.WriteString("\n")
	}
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
	providers map[string]FetchProvider
}

// Def returns the tool definition.
func (t *WebFetchTool) Def() provider.ToolDef {
	// Build available sources list dynamically.
	sources := make([]string, 0, len(t.providers))
	for name, p := range t.providers {
		if p.Available() {
			sources = append(sources, name)
		}
	}
	sort.Strings(sources)
	sourceDesc := fmt.Sprintf("Fetch source. Available: %s. Default: direct. Use 'jina' for anti-bot bypass (returns clean markdown).", strings.Join(sources, ", "))

	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_fetch",
			Description: "Fetch the content of a web page. Returns the text content (HTML tags stripped for readability). Content is cached for 10 minutes — repeated fetches of the same URL are free. Use offset/limit to paginate through long pages without re-fetching. If a site returns 403/503 (anti-bot), retry with a different source (e.g. jina or browser).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to fetch.",
					},
					"source": map[string]any{
						"type":        "string",
						"description": sourceDesc,
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
	URL    string `json:"url"`
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
		source = "direct"
	}

	p, ok := t.providers[source]
	if !ok || !p.Available() {
		available := t.availableSources()
		if ok && !p.Available() {
			return fmt.Sprintf("Error: fetch source %q is not available. Available: %s", source, strings.Join(available, ", "))
		}
		return fmt.Sprintf("Error: unknown fetch source %q. Available: %s", source, strings.Join(available, ", "))
	}

	cacheKey := a.URL + "::" + source

	// Check cache
	content, cached := webFetchCacheLookup(cacheKey)
	if !cached {
		content, err = p.Fetch(ctx, a.URL)
		if err != nil {
			// On 403/503, hint available sources.
			if httpErr, ok := err.(*HTTPError); ok && (httpErr.StatusCode == 403 || httpErr.StatusCode == 503) {
				available := t.availableSources()
				return fmt.Sprintf("Error: %v. Try a different source to bypass anti-bot protection. Available: %s", err, strings.Join(available, ", "))
			}
			return fmt.Sprintf("Error: %v", err)
		}

		// Jina returns markdown — skip HTML extraction.
		// Direct and browser return HTML — extract text.
		if source != "jina" {
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

	fields := map[string]any{
		"url":         a.URL,
		"source":      source,
		"total_chars": totalChars,
		"showing":     fmt.Sprintf("%d-%d", offset, end),
	}
	if cached {
		fields["cached"] = true
	}
	if end < totalChars {
		fields["next_offset"] = end
	}

	return toolResult("web_fetch", fields, slice)
}

func (t *WebFetchTool) availableSources() []string {
	available := make([]string, 0, len(t.providers))
	for name, prov := range t.providers {
		if prov.Available() {
			available = append(available, name)
		}
	}
	sort.Strings(available)
	return available
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
