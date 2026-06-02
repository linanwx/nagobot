package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// stubResultsProvider returns a fixed result set, letting tests drive the
// results>0 vs results==0 branches of WebSearchTool.Run.
type stubResultsProvider struct {
	results []SearchResult
}

func (s *stubResultsProvider) Name() string   { return "brave" }
func (s *stubResultsProvider) Tags() []string { return []string{"paid"} }
func (s *stubResultsProvider) Available() bool { return true }
func (s *stubResultsProvider) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return s.results, nil
}

func runWebSearch(results []SearchResult) string {
	provs := map[string]SearchProvider{"brave": &stubResultsProvider{results: results}}
	tool := &WebSearchTool{providers: provs, healthChecker: NewSearchHealthChecker(provs)}
	args, _ := json.Marshal(map[string]any{"source": "brave", "query": "q"})
	return tool.Run(context.Background(), args)
}

// TestWebSearchOmitsSourceStatusWhenResultsFound verifies the volume trim:
// a successful search (results>0) must NOT carry the source_status header,
// which previously listed every provider's health on every result.
func TestWebSearchOmitsSourceStatusWhenResultsFound(t *testing.T) {
	out := runWebSearch([]SearchResult{{Title: "T", URL: "http://example.com", Snippet: "s"}})
	if strings.Contains(out, "source_status") {
		t.Errorf("results>0 result must omit source_status; got:\n%s", out)
	}
	if !strings.Contains(out, "results: 1") {
		t.Errorf("expected results: 1 header; got:\n%s", out)
	}
}

// TestWebSearchKeepsProviderStatsWhenEmpty verifies provider health is still
// surfaced where it is actionable — the empty-results path keeps Provider stats.
func TestWebSearchKeepsProviderStatsWhenEmpty(t *testing.T) {
	out := runWebSearch(nil)
	if !strings.Contains(out, "Provider stats") {
		t.Errorf("empty-results path must keep Provider stats (DetailedStatus); got:\n%s", out)
	}
}
