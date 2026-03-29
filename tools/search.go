package tools

import (
	"context"
	"fmt"
	"strings"
)

// SearchResult represents a single search result.
type SearchResult struct {
	Title       string
	URL         string
	Snippet     string
	PublishDate string // optional: publication date
	Source      string // optional: source website name
}

// SearchProvider is the interface for pluggable search backends.
type SearchProvider interface {
	// Name returns the provider identifier (e.g. "duckduckgo", "brave").
	Name() string
	// Tags returns descriptive labels (e.g. "free", "paid", "low quality").
	Tags() []string
	// Available reports whether the provider can serve requests right now.
	// This is checked at call time to support hot-reloading of config (e.g. API keys).
	Available() bool
	// Search performs a web search and returns results.
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
}

// formatSearchResults formats search results into a human-readable string.
func FormatSearchResults(query string, results []SearchResult) string {
	if len(results) == 0 {
		return "No search results found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", query))
	for i, r := range results {
		title := r.Title
		if r.Source != "" {
			title += fmt.Sprintf(" [%s]", r.Source)
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, title, r.URL))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
		}
		if r.PublishDate != "" {
			sb.WriteString(fmt.Sprintf("   Published: %s\n", r.PublishDate))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
