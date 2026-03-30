package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// FetchProvider is the interface for pluggable web fetch backends.
type FetchProvider interface {
	// Name returns the provider identifier (e.g. "direct", "jina").
	Name() string
	// Tags returns descriptive labels (e.g. "free", "limited-time").
	Tags() []string
	// Available reports whether the provider can serve requests right now.
	Available() bool
	// Fetch fetches the content of a URL.
	// Providers that return clean text/markdown should do so directly.
	// Providers that return raw HTML will have extractTextContent applied by the caller.
	Fetch(ctx context.Context, url string) (content string, err error)
	// ReturnsMarkdown reports whether Fetch returns clean markdown/text (true)
	// or raw HTML that needs extractTextContent (false).
	ReturnsMarkdown() bool
}

// ---------- direct ----------

// DirectFetchProvider fetches pages with a plain HTTP GET and strips HTML tags.
type DirectFetchProvider struct{}

func (p *DirectFetchProvider) Name() string            { return "raw" }
func (p *DirectFetchProvider) Tags() []string          { return []string{"free", "no anti-bot bypass"} }
func (p *DirectFetchProvider) Available() bool         { return true }
func (p *DirectFetchProvider) ReturnsMarkdown() bool   { return false }

func (p *DirectFetchProvider) Fetch(ctx context.Context, rawURL string) (string, error) {
	client := &http.Client{Timeout: webFetchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxReadBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// HTTPError represents a non-200 HTTP response.
type HTTPError struct {
	StatusCode int
	Status     string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d %s", e.StatusCode, e.Status)
}

// ---------- jina ----------

// JinaFetchProvider fetches pages via the Jina Reader API (r.jina.ai).
// Returns clean markdown. Free with optional API key for higher rate limits.
type JinaFetchProvider struct {
	KeyFn func() string
}

func (p *JinaFetchProvider) Name() string      { return "jina" }
func (p *JinaFetchProvider) Tags() []string    { return []string{"free", "rate-limited"} }
func (p *JinaFetchProvider) Available() bool   { return true }
func (p *JinaFetchProvider) ReturnsMarkdown() bool { return true }

func (p *JinaFetchProvider) Fetch(ctx context.Context, rawURL string) (string, error) {
	jinaURL := "https://r.jina.ai/" + rawURL

	client := &http.Client{Timeout: webFetchHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, "GET", jinaURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")

	if p.KeyFn != nil {
		if key := p.KeyFn(); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jina reader: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxReadBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
