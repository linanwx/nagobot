package tools

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// ReadabilityFetchProvider fetches pages with HTTP GET, extracts main content
// via go-readability, and converts to Markdown via html-to-markdown.
type ReadabilityFetchProvider struct{}

func (p *ReadabilityFetchProvider) Name() string            { return "go-readability" }
func (p *ReadabilityFetchProvider) Tags() []string          { return []string{"free", "no anti-bot bypass"} }
func (p *ReadabilityFetchProvider) Available() bool         { return true }
func (p *ReadabilityFetchProvider) ReturnsMarkdown() bool   { return true }

func (p *ReadabilityFetchProvider) Fetch(ctx context.Context, rawURL string) (string, error) {
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

	// Diverted before go-readability sees it: handed binary, the library reports
	// a generic extraction failure, which grades as ContentError and reads as
	// "try another source" when the real answer is "this is a file".
	reader, nonPage, err := saveIfNotPage(ctx, resp, rawURL)
	if err != nil {
		return "", err
	}
	if nonPage != nil {
		return "", nonPage
	}

	body := io.LimitReader(reader, webFetchMaxReadBytes)

	parsedURL, _ := url.Parse(rawURL)
	article, err := readability.FromReader(body, parsedURL)
	if err != nil {
		// HTTP 200, but no article could be extracted (the library reports
		// "the Node field is nil" for pages it cannot find a body in). The host
		// is fine; the page is just not readable this way.
		return "", &ContentError{Err: err}
	}

	// Render extracted content to HTML, then convert to Markdown.
	var htmlBuf bytes.Buffer
	if err := article.RenderHTML(&htmlBuf); err != nil {
		// Fallback to plain text.
		var textBuf bytes.Buffer
		if err := article.RenderText(&textBuf); err != nil {
			return "", &ContentError{Err: err}
		}
		return textBuf.String(), nil
	}

	var sb strings.Builder
	if title := article.Title(); title != "" {
		sb.WriteString("# ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
	}

	md, err := htmltomd.ConvertString(htmlBuf.String())
	if err != nil {
		// Fallback to plain text if markdown conversion fails.
		var textBuf bytes.Buffer
		if err := article.RenderText(&textBuf); err == nil {
			sb.WriteString(textBuf.String())
		}
		return sb.String(), nil
	}
	sb.WriteString(md)

	return sb.String(), nil
}
