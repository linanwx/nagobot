package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestBraveSearchLive hits the real Brave API to confirm the refactored
// pool/round-robin request path works end-to-end. Gated behind
// NAGOBOT_LIVE_BRAVE=<key or comma-separated pool>.
//
//	NAGOBOT_LIVE_BRAVE="$key" go test ./tools -run TestBraveSearchLive -v
func TestBraveSearchLive(t *testing.T) {
	pool := strings.TrimSpace(os.Getenv("NAGOBOT_LIVE_BRAVE"))
	if pool == "" {
		t.Skip("set NAGOBOT_LIVE_BRAVE=<key|pool> to run the live brave search e2e")
	}
	resetBraveState()
	defer resetBraveState()

	p := &BraveSearchProvider{KeyFn: func() string { return pool }}
	if !p.Available() {
		t.Fatal("provider reports not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := p.Search(ctx, "anthropic claude", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	t.Logf("got %d results; first: %q (%s)", len(results), results[0].Title, results[0].URL)
}
