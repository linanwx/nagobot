package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// webFetchMaxExtractBytes is the input budget for the extractor, and it is
// deliberately much larger than webFetchMaxReadBytes.
//
// For raw/jina/kimi the body IS the output, so capping the read caps what
// reaches the model. For an extractor it does not: this provider turns a page
// into its article, and what actually reaches the model is bounded downstream
// by the offset/limit pagination in WebFetchTool.Run. So the 500 KB read cap
// bought no context at all — it just cut large documents off before their
// article body, and readability then reports the page as having no article.
// The size is measured, not guessed. Replayed against the 642 real URLs this
// deployment has fetched (242 that failed this way plus a 400-page sample that
// succeeded): the largest page was 5.97 MB and NONE reached 8 MB, so the budget
// clears observed traffic with headroom and raising it further would recover
// nothing. Parse is linear and cheap (47 ms and ~21 MB transient at 3.46 MB),
// and the download is already bounded by webFetchHTTPTimeout. It is not set
// higher because the transient DOM is several times the input and up to 16
// threads can fetch at once.
const webFetchMaxExtractBytes = 8 << 20

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

	// Read one byte past the budget so truncation is DETECTABLE. io.LimitReader
	// gives no signal that it clipped, and a document cut mid-body still parses
	// — into an article with no content, which is indistinguishable from a page
	// that genuinely has none unless we remember that we did the cutting.
	raw, err := io.ReadAll(io.LimitReader(reader, webFetchMaxExtractBytes+1))
	if err != nil {
		return "", err
	}
	truncated := len(raw) > webFetchMaxExtractBytes
	if truncated {
		raw = raw[:webFetchMaxExtractBytes]
	}

	parsedURL, _ := url.Parse(rawURL)
	article, err := readability.FromReader(bytes.NewReader(raw), parsedURL)
	if err != nil {
		// HTTP 200, but the document could not be parsed at all. The host is
		// fine; the page is just not readable this way.
		return "", &ContentError{Err: err}
	}

	// FromReader reports NO error for a page it found no article in: it returns
	// an Article whose Node is nil, and the miss only surfaces as the library's
	// internal "the Node field is nil" from whichever render runs first. Both
	// renders test that same field, so the RenderText fallback below can never
	// rescue this case — it just relabels one library message as another. Say
	// what actually happened instead, and separate the two causes, because they
	// call for opposite responses: another source cannot fix a page that has no
	// article, and no source needs to when the page was simply too big for us.
	if article.Node == nil {
		if truncated {
			return "", &ContentError{Err: fmt.Errorf("page is larger than the %d-byte extraction budget and its article body was cut off", webFetchMaxExtractBytes)}
		}
		return "", &ContentError{Err: fmt.Errorf("no article body found in this page")}
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
