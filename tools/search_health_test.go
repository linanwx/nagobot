package tools

import (
	"context"
	"strings"
	"testing"
)

// fakeSearchProvider is a test helper implementing SearchProvider.
type fakeSearchProvider struct {
	name      string
	tags      []string
	available bool
}

func (f *fakeSearchProvider) Name() string   { return f.name }
func (f *fakeSearchProvider) Tags() []string { return f.tags }
func (f *fakeSearchProvider) Available() bool { return f.available }
func (f *fakeSearchProvider) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return nil, nil
}

func TestDetailedStatus_ShowsUnavailableProviders(t *testing.T) {
	providers := map[string]SearchProvider{
		"duckduckgo": &fakeSearchProvider{name: "duckduckgo", tags: []string{"free"}, available: true},
		"bing":       &fakeSearchProvider{name: "bing", tags: []string{"free"}, available: true},
		"brave":      &fakeSearchProvider{name: "brave", tags: []string{"paid"}, available: false},
		"zhipu":      &fakeSearchProvider{name: "zhipu", available: false},
	}
	hc := NewSearchHealthChecker(providers)

	// Record some data so available providers show up
	hc.Record("duckduckgo", true, 5, 200)

	status := hc.DetailedStatus()

	// Should mention unavailable providers
	if !strings.Contains(status, "unavailable") {
		t.Errorf("DetailedStatus should mention unavailable providers, got:\n%s", status)
	}
	if !strings.Contains(status, "brave") {
		t.Errorf("DetailedStatus should mention brave as unavailable, got:\n%s", status)
	}
	if !strings.Contains(status, "zhipu") {
		t.Errorf("DetailedStatus should mention zhipu as unavailable, got:\n%s", status)
	}
	if !strings.Contains(status, "API key") {
		t.Errorf("DetailedStatus should hint about API key, got:\n%s", status)
	}
}

func TestDetailedStatus_NoUnavailableProviders(t *testing.T) {
	providers := map[string]SearchProvider{
		"duckduckgo": &fakeSearchProvider{name: "duckduckgo", available: true},
		"bing":       &fakeSearchProvider{name: "bing", available: true},
	}
	hc := NewSearchHealthChecker(providers)
	hc.Record("duckduckgo", true, 5, 200)

	status := hc.DetailedStatus()

	// Should NOT mention unavailable when all are available
	if strings.Contains(status, "unavailable") {
		t.Errorf("DetailedStatus should not mention unavailable when all providers are available, got:\n%s", status)
	}
}

func TestDetailedStatus_AllUnavailable(t *testing.T) {
	providers := map[string]SearchProvider{
		"brave": &fakeSearchProvider{name: "brave", available: false},
		"zhipu": &fakeSearchProvider{name: "zhipu", available: false},
	}
	hc := NewSearchHealthChecker(providers)

	status := hc.DetailedStatus()

	// Should clearly state no available providers and list unavailable ones
	if !strings.Contains(status, "No available") {
		t.Errorf("DetailedStatus should say no available providers, got:\n%s", status)
	}
	if !strings.Contains(status, "brave") || !strings.Contains(status, "zhipu") {
		t.Errorf("DetailedStatus should list unavailable providers, got:\n%s", status)
	}
}
