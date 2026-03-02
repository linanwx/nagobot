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
}

// Def returns the tool definition.
func (t *WebSearchTool) Def() provider.ToolDef {
	// Build available sources list dynamically (only providers that are ready right now)
	sources := make([]string, 0, len(t.providers))
	for name, p := range t.providers {
		if p.Available() {
			sources = append(sources, name)
		}
	}
	sort.Strings(sources)
	sourceDesc := fmt.Sprintf("Search source. Available: %s. Default: duckduckgo.", strings.Join(sources, ", "))

	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "web_search",
			Description: "Search the web and return results. Use for finding current information, documentation, etc.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query.",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results to return. If omitted, uses the system-configured default.",
					},
					"source": map[string]any{
						"type":        "string",
						"description": sourceDesc,
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
		source = "duckduckgo"
	}

	p, ok := t.providers[source]
	if !ok || !p.Available() {
		available := make([]string, 0, len(t.providers))
		for name, prov := range t.providers {
			if prov.Available() {
				available = append(available, name)
			}
		}
		sort.Strings(available)
		if ok && !p.Available() {
			return fmt.Sprintf("Error: search source %q is not available (API key not configured). Use the manage-search skill to set it up. Available: %s", source, strings.Join(available, ", "))
		}
		return fmt.Sprintf("Error: unknown search source %q. Available: %s", source, strings.Join(available, ", "))
	}

	results, err := p.Search(ctx, a.Query, a.MaxResults)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return FormatSearchResults(a.Query, results)
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
					"raw": map[string]any{
						"type":        "boolean",
						"description": "Set to true to return raw HTML instead of stripped text. Can be omitted.",
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
	Raw    bool   `json:"raw,omitempty"`
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

	// Build cache key: url + source + raw flag
	cacheKey := a.URL + "::" + source
	if a.Raw {
		cacheKey += "::raw"
	}

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
		// Direct and browser return HTML — extract text unless raw.
		if !a.Raw && source != "jina" {
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
		return fmt.Sprintf("[Total: %d chars | offset %d is beyond end of content]", totalChars, offset)
	}

	end := offset + limit
	if end > totalChars {
		end = totalChars
	}
	slice := content[offset:end]

	// Build header with pagination info
	var header string
	switch {
	case cached:
		header = fmt.Sprintf("[Cached | Total: %d chars | Showing: %d–%d]", totalChars, offset, end)
	case source != "direct":
		header = fmt.Sprintf("[via %s | Total: %d chars | Showing: %d–%d]", source, totalChars, offset, end)
	default:
		header = fmt.Sprintf("[Total: %d chars | Showing: %d–%d]", totalChars, offset, end)
	}
	if end < totalChars {
		header += fmt.Sprintf(" [Next page: offset=%d]", end)
	}

	return header + "\n" + slice
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
