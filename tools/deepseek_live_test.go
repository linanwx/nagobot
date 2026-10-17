package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
)

// Explicit opt-in because these calls use the official, billable tool APIs.
func TestDeepSeekLiveSearchFetch(t *testing.T) {
	if os.Getenv("NAGOBOT_DEEPSEEK_WEB_LIVE") != "1" {
		t.Skip("set NAGOBOT_DEEPSEEK_WEB_LIVE=1 for paid live API verification")
	}
	cfg, err := config.Load()
	key := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if key == "" && err == nil {
		if pc := cfg.Providers.GetProviderConfig("deepseek"); pc != nil {
			key = strings.TrimSpace(pc.APIKey)
		}
	}
	if key == "" {
		t.Fatal("live verification requires official DeepSeek API key")
	}
	keyFn := func() string { return key }
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	search := &DeepSeekSearchProvider{KeyFn: keyFn}
	results, err := search.Search(ctx, "site:go.dev Go programming language", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].URL == "" || results[0].Title == "" {
		t.Fatal("no usable live search results")
	}
	fetch := &DeepSeekFetchProvider{KeyFn: keyFn}
	content, err := fetch.Fetch(ctx, results[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(content)) < 100 {
		t.Fatal("live fetch did not extract substantive content")
	}
	t.Logf("official Search returned %d results; Fetch extracted %d bytes from %s", len(results), len(content), results[0].URL)
}
