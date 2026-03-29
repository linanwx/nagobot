package tools

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
)

// loadTestConfig loads config and returns search keys.
// Skips the test if config is not available.
func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	// Set config dir to user's actual config (integration test).
	home, _ := os.UserHomeDir()
	os.Setenv("NAGOBOT_CONFIG_DIR", home+"/.nagobot")
	defer os.Unsetenv("NAGOBOT_CONFIG_DIR")

	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config not available: %v", err)
	}
	return cfg
}

func TestIntegration_BraveSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadTestConfig(t)
	key := cfg.GetSearchKey("brave")
	if key == "" {
		t.Skip("brave search key not configured")
	}

	p := &BraveSearchProvider{KeyFn: func() string { return key }}
	if !p.Available() {
		t.Fatal("provider should be available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	results, err := p.Search(ctx, "Go programming language", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	r := results[0]
	t.Logf("Brave result: %s — %s", r.Title, r.URL)
	if r.Title == "" || r.URL == "" {
		t.Error("result missing title or URL")
	}
}

func TestIntegration_OpenSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadTestConfig(t)
	key := cfg.GetSearchKey("opensearch")
	host := cfg.GetSearchKey("opensearch-host")
	if key == "" || host == "" {
		t.Skip("opensearch key or host not configured")
	}

	p := &OpenSearchProvider{
		KeyFn:  func() string { return key },
		HostFn: func() string { return host },
	}
	if !p.Available() {
		t.Fatal("provider should be available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	results, err := p.Search(ctx, "Go programming language", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	r := results[0]
	t.Logf("OpenSearch result: %s — %s", r.Title, r.URL)
	if r.Title == "" || r.URL == "" {
		t.Error("result missing title or URL")
	}
}

func TestIntegration_ZhipuSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadTestConfig(t)
	// Same fallback logic as thread_runtime.go
	key := cfg.GetSearchKey("zhipu")
	if key == "" {
		if pc := cfg.Providers.GetProviderConfig("zhipu-cn"); pc != nil {
			key = pc.APIKey
		}
	}
	if key == "" {
		t.Skip("zhipu search key not configured")
	}

	p := &ZhipuSearchProvider{KeyFn: func() string { return key }, ProviderName: "zhipu-cn-std", Engine: "search_std"}
	if !p.Available() {
		t.Fatal("provider should be available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	results, err := p.Search(ctx, "Go programming language", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	r := results[0]
	t.Logf("Zhipu result: %s — %s", r.Title, r.URL)
	if r.Title == "" || r.URL == "" {
		t.Error("result missing title or URL")
	}
}
