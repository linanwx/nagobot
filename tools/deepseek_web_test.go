package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type deepSeekTestTransport func(*http.Request) (*http.Response, error)

func (f deepSeekTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func deepSeekTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestDeepSeekSearchProtocol(t *testing.T) {
	key := "first-key"
	calls := 0
	p := &DeepSeekSearchProvider{KeyFn: func() string { return key }}
	p.transport = deepSeekTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != "https://api.deepseek.com/v1/tools/search" {
			t.Fatalf("unexpected destination: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.TrimSpace(key) || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("incorrect authentication or content type")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 3 || body["query"] != `site:example.com "中文" -site:other.com` || body["count"] != float64(2) || body["need_content"] != false {
			t.Fatalf("unexpected body: %#v", body)
		}
		return deepSeekTestResponse(200, `{"results":[{"title":"one","url":"https://example.com/1","summary":"long summary","snippet":"short","site_name":null,"date":null},{"title":"two","url":"https://example.com/2","summary":" ","snippet":"fallback","site_name":"Example","date":"2026-10-17T00:00:00Z"},{"title":"extra"}]}`), nil
	})
	for _, nextKey := range []string{"first-key", " second-key "} {
		key = nextKey
		results, err := p.Search(context.Background(), `site:example.com "中文" -site:other.com`, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 || results[0].Snippet != "long summary" || results[0].Source != "" || results[0].PublishDate != "" || results[1].Snippet != "fallback" || results[1].Source != "Example" || results[1].PublishDate != "2026-10-17T00:00:00Z" {
			t.Fatalf("unexpected mapping: %#v", results)
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestDeepSeekSearchValidation(t *testing.T) {
	calls := 0
	p := &DeepSeekSearchProvider{KeyFn: func() string { return "key" }, transport: deepSeekTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return deepSeekTestResponse(200, `{"results":[]}`), nil
	})}
	for _, tc := range []struct {
		query string
		count int
	}{{"", 1}, {" \t", 1}, {strings.Repeat("中", 129), 1}, {string([]byte{0xff}), 1}, {"q", 0}, {"q", 51}} {
		if _, err := p.Search(context.Background(), tc.query, tc.count); err == nil {
			t.Errorf("accepted invalid query/count: %q/%d", tc.query, tc.count)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached transport")
	}
	results, err := p.Search(context.Background(), strings.Repeat("中", 128), 50)
	if err != nil || len(results) != 0 || calls != 1 {
		t.Fatalf("Unicode boundary rejected: results=%v err=%v calls=%d", results, err, calls)
	}
}

func TestDeepSeekCredentials(t *testing.T) {
	for _, keyFn := range []func() string{nil, func() string { return " \n" }} {
		s := &DeepSeekSearchProvider{KeyFn: keyFn}
		f := &DeepSeekFetchProvider{KeyFn: keyFn}
		if s.Available() || f.Available() {
			t.Fatal("empty key advertised available")
		}
		if _, err := s.Search(context.Background(), "query", 1); err == nil {
			t.Fatal("search accepted missing key")
		}
		if _, err := f.Fetch(context.Background(), "https://example.com"); err == nil {
			t.Fatal("fetch accepted missing key")
		}
	}
}

func TestDeepSeekFetchProtocolAndValidation(t *testing.T) {
	calls := 0
	markdown := "# title\n\n[link](https://example.com)\n"
	p := &DeepSeekFetchProvider{KeyFn: func() string { return "key" }, transport: deepSeekTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != "https://api.deepseek.com/v1/tools/fetch" || r.Header.Get("Authorization") != "Bearer key" {
			t.Fatal("incorrect fetch destination/auth")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["url"] != "https://example.com/page?q=中文" {
			t.Fatalf("body=%#v", body)
		}
		data, _ := json.Marshal(map[string]string{"title": "title", "content": markdown})
		return deepSeekTestResponse(200, string(data)), nil
	})}
	for _, u := range []string{"", "file:///tmp/a", "https:///missing", "https://user:pass@example.com", "https://example.com/" + strings.Repeat("a", 8192), "https://example.com/" + string([]byte{0xff})} {
		if _, err := p.Fetch(context.Background(), u); err == nil {
			t.Errorf("accepted invalid URL %q", u)
		}
	}
	if calls != 0 {
		t.Fatal("invalid URL reached transport")
	}
	got, err := p.Fetch(context.Background(), "https://example.com/page?q=中文")
	if err != nil || got != markdown || !p.ReturnsMarkdown() {
		t.Fatalf("markdown=%q err=%v", got, err)
	}
}

func TestDeepSeekResponseFailures(t *testing.T) {
	for _, body := range []string{"", `{`, `{}`, `null`, `{"results":null}`, `{"results":{}}`, strings.Repeat(" ", deepSeekToolsMaxResponse+1)} {
		p := &DeepSeekSearchProvider{KeyFn: func() string { return "key" }, transport: deepSeekTestTransport(func(*http.Request) (*http.Response, error) { return deepSeekTestResponse(200, body), nil })}
		if _, err := p.Search(context.Background(), "q", 1); err == nil {
			t.Errorf("accepted invalid response length=%d", len(body))
		}
	}
	for _, body := range []string{`{}`, `null`, `{"content":" \n"}`} {
		p := &DeepSeekFetchProvider{KeyFn: func() string { return "key" }, transport: deepSeekTestTransport(func(*http.Request) (*http.Response, error) { return deepSeekTestResponse(200, body), nil })}
		_, err := p.Fetch(context.Background(), "https://example.com")
		var contentErr *ContentError
		if !errors.As(err, &contentErr) {
			t.Errorf("empty content should be ContentError, got %v", err)
		}
	}
}

func TestDeepSeekHTTPFailuresAndRedirect(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 413, 302, 307} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			p := &DeepSeekSearchProvider{KeyFn: func() string { return "key" }, transport: deepSeekTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				response := deepSeekTestResponse(status, `{"error":{"message":"secret upstream error"}}`)
				response.Header.Set("Location", "https://untrusted.example/steal")
				return response, nil
			})}
			_, err := p.Search(context.Background(), "q", 1)
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if strings.Contains(err.Error(), "secret upstream error") {
				t.Fatal("untrusted upstream body leaked")
			}
		})
	}
}

func TestDeepSeekRetryAndCancellation(t *testing.T) {
	calls := 0
	p := &DeepSeekSearchProvider{KeyFn: func() string { return "key" }, transport: deepSeekTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return deepSeekTestResponse(429, `{}`), nil
		}
		return deepSeekTestResponse(200, `{"results":[]}`), nil
	})}
	if _, err := p.Search(context.Background(), "q", 1); err != nil || calls != 2 {
		t.Fatalf("retry err=%v calls=%d", err, calls)
	}
	for _, status := range []int{408, 429, 500, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			p.transport = deepSeekTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				cancel()
				return deepSeekTestResponse(status, `{}`), nil
			})
			started := time.Now()
			_, err := p.Search(ctx, "q", 1)
			if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(started) > 500*time.Millisecond {
				t.Fatalf("cancellation err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestDeepSeekFetchCachePagination(t *testing.T) {
	calls := 0
	p := &DeepSeekFetchProvider{KeyFn: func() string { return "key" }, transport: deepSeekTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return deepSeekTestResponse(200, `{"title":"page","content":"firstsecond"}`), nil
	})}
	tool := &WebFetchTool{providers: map[string]FetchProvider{"deepseek": p}}
	target := fmt.Sprintf("https://example.com/deepseek-cache-%d", time.Now().UnixNano())
	for _, tc := range []struct {
		offset int
		want   string
	}{{0, "first"}, {5, "second"}} {
		args, _ := json.Marshal(map[string]any{"url": target, "source": "deepseek", "offset": tc.offset, "limit": len(tc.want)})
		result := tool.Run(context.Background(), args)
		if !strings.Contains(result, tc.want) {
			t.Fatalf("page missing %q: %s", tc.want, result)
		}
	}
	if calls != 1 {
		t.Fatalf("pagination incurred %d paid fetches", calls)
	}
}
