package tools

import (
	"testing"
)

func TestZhipuProviderName(t *testing.T) {
	p := &ZhipuSearchProvider{}
	if got := p.Name(); got != "zhipu" {
		t.Errorf("Name() = %q, want %q", got, "zhipu")
	}
}

func TestZhipuProviderAvailable(t *testing.T) {
	tests := []struct {
		name  string
		keyFn func() string
		want  bool
	}{
		{"nil KeyFn", nil, false},
		{"empty key", func() string { return "" }, false},
		{"valid key", func() string { return "key-123" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ZhipuSearchProvider{KeyFn: tt.keyFn}
			if got := p.Available(); got != tt.want {
				t.Errorf("Available() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseZhipuResults(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		maxResults int
		wantCount  int
		wantErr    bool
		wantFirst  *SearchResult
	}{
		{
			name: "valid response with results",
			data: `{
				"search_result": [
					{"title": "Go Programming", "link": "https://go.dev", "content": "The Go programming language", "media": "go.dev", "publish_date": "2024-01-15"},
					{"title": "Go Tutorial", "link": "https://go.dev/tour", "content": "A Tour of Go", "media": "go.dev", "publish_date": "2024-02-01"}
				]
			}`,
			maxResults: 10,
			wantCount:  2,
			wantFirst: &SearchResult{
				Title:       "Go Programming",
				URL:         "https://go.dev",
				Snippet:     "The Go programming language",
				PublishDate: "2024-01-15",
				Source:      "go.dev",
			},
		},
		{
			name: "maxResults caps output",
			data: `{
				"search_result": [
					{"title": "Result 1", "link": "https://example.com/1", "content": "First"},
					{"title": "Result 2", "link": "https://example.com/2", "content": "Second"},
					{"title": "Result 3", "link": "https://example.com/3", "content": "Third"}
				]
			}`,
			maxResults: 2,
			wantCount:  2,
		},
		{
			name:       "empty results",
			data:       `{"search_result": []}`,
			maxResults: 10,
			wantCount:  0,
		},
		{
			name:       "null search_result",
			data:       `{}`,
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
			results, err := parseZhipuResults([]byte(tt.data), tt.maxResults)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseZhipuResults() error = %v, wantErr %v", err, tt.wantErr)
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
					t.Errorf("Source = %q, want %q", got.Source, tt.wantFirst.Source)
				}
			}
		})
	}
}
