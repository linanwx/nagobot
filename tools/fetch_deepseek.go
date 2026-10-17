package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// DeepSeekFetchProvider returns remote-extracted Markdown through the official API.
type DeepSeekFetchProvider struct {
	KeyFn     func() string
	transport http.RoundTripper
}

func (p *DeepSeekFetchProvider) Name() string { return "deepseek" }
func (p *DeepSeekFetchProvider) Tags() []string {
	return []string{"paid", "¥5/1k successful nonempty requests", "official DeepSeek key", "5 QPS/account", "remote markdown"}
}
func (p *DeepSeekFetchProvider) Available() bool {
	return p.KeyFn != nil && strings.TrimSpace(p.KeyFn()) != ""
}
func (p *DeepSeekFetchProvider) ReturnsMarkdown() bool { return true }
func (p *DeepSeekFetchProvider) Fetch(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || !utf8.ValidString(rawURL) || utf8.RuneCountInString(rawURL) > 8192 {
		return "", fmt.Errorf("deepseek fetch: URL must be HTTP(S), contain a host, contain no credentials, and be at most 8192 characters")
	}
	var response struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := deepSeekToolsRequest(ctx, p.KeyFn, "fetch", struct {
		URL string `json:"url"`
	}{rawURL}, &response, webFetchHTTPTimeout, p.transport); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Content) == "" {
		return "", &ContentError{Err: fmt.Errorf("deepseek fetch: empty extracted content")}
	}
	return response.Content, nil
}
