package tools

import (
	"testing"
)

func TestOpenSearchProviderName(t *testing.T) {
	p := &OpenSearchProvider{}
	if got := p.Name(); got != "opensearch" {
		t.Errorf("Name() = %q, want %q", got, "opensearch")
	}
}

func TestOpenSearchProviderAvailable(t *testing.T) {
	tests := []struct {
		name   string
		keyFn  func() string
		hostFn func() string
		want   bool
	}{
		{"nil KeyFn and nil HostFn", nil, nil, false},
		{"empty key", func() string { return "" }, func() string { return "default-j01.platform-cn-shanghai.opensearch.aliyuncs.com" }, false},
		{"nil HostFn", func() string { return "key-123" }, nil, false},
		{"empty host", func() string { return "key-123" }, func() string { return "" }, false},
		{"both valid", func() string { return "key-123" }, func() string { return "default-j01.platform-cn-shanghai.opensearch.aliyuncs.com" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &OpenSearchProvider{KeyFn: tt.keyFn, HostFn: tt.hostFn}
			if got := p.Available(); got != tt.want {
				t.Errorf("Available() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseOpenSearchResults(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		maxResults int
		wantCount  int
		wantErr    bool
		wantFirst  *SearchResult // check first result if non-nil
	}{
		{
			name: "valid response with results",
			data: `{
				"result": {
					"search_result": [
						{"title": "Go Programming", "link": "https://go.dev", "snippet": "The Go programming language", "meta_info": {"publishedTime": "2024-01-15"}},
						{"title": "Go Tutorial", "link": "https://go.dev/tour", "snippet": "A Tour of Go", "meta_info": {"publishedTime": "2024-02-01"}}
					]
				}
			}`,
			maxResults: 10,
			wantCount:  2,
			wantFirst: &SearchResult{
				Title:       "Go Programming",
				URL:         "https://go.dev",
				Snippet:     "The Go programming language",
				PublishDate: "2024-01-15",
			},
		},
		{
			name: "maxResults caps output",
			data: `{
				"result": {
					"search_result": [
						{"title": "Result 1", "link": "https://example.com/1", "snippet": "First"},
						{"title": "Result 2", "link": "https://example.com/2", "snippet": "Second"},
						{"title": "Result 3", "link": "https://example.com/3", "snippet": "Third"}
					]
				}
			}`,
			maxResults: 2,
			wantCount:  2,
		},
		{
			name:       "empty results",
			data:       `{"result": {"search_result": []}}`,
			maxResults: 10,
			wantCount:  0,
		},
		{
			name:       "null search_result",
			data:       `{"result": {}}`,
			maxResults: 10,
			wantCount:  0,
		},
		{
			name:       "invalid JSON",
			data:       `{not valid json`,
			maxResults: 10,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := parseOpenSearchResults([]byte(tt.data), tt.maxResults)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseOpenSearchResults() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(results) != tt.wantCount {
				t.Errorf("got %d results, want %d", len(results), tt.wantCount)
			}
			if tt.wantFirst != nil && len(results) > 0 {
				got := results[0]
				if got.Title != tt.wantFirst.Title {
					t.Errorf("Title = %q, want %q", got.Title, tt.wantFirst.Title)
				}
				if got.URL != tt.wantFirst.URL {
					t.Errorf("URL = %q, want %q", got.URL, tt.wantFirst.URL)
				}
				if got.Snippet != tt.wantFirst.Snippet {
					t.Errorf("Snippet = %q, want %q", got.Snippet, tt.wantFirst.Snippet)
				}
				if got.PublishDate != tt.wantFirst.PublishDate {
					t.Errorf("PublishDate = %q, want %q", got.PublishDate, tt.wantFirst.PublishDate)
				}
				if got.Source != tt.wantFirst.Source {
					t.Errorf("Source = %q, want %q", got.Source, tt.wantFirst.Source) // Source is always empty for OpenSearch
				}
			}
		})
	}
}
