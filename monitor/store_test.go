package monitor

import (
	"os"
	"testing"
	"time"
)

func TestStoreRecordAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	now := time.Now()
	store.Record(TurnRecord{
		Timestamp:  now,
		DurationMs: 1500,
		Provider:   "openrouter",
		Model:      "moonshotai/kimi-k2.6",
		Agent:      "soul",
		SessionKey: "telegram:123",
		Iterations: 2,
		ToolCalls:  3,
		AccTotalTokens: 500,
	})
	store.Record(TurnRecord{
		Timestamp:  now.Add(-2 * time.Hour),
		DurationMs: 3000,
		Provider:   "deepseek",
		Model:      "deepseek-v4-flash",
		Agent:      "soul",
		SessionKey: "discord:456",
		Iterations: 1,
		ToolCalls:  0,
		AccTotalTokens: 200,
	})

	// Load all
	records := store.Load(time.Time{})
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Load with cutoff (last hour)
	recent := store.Load(now.Add(-time.Hour))
	if len(recent) != 1 {
		t.Fatalf("expected 1 recent record, got %d", len(recent))
	}
	if recent[0].Provider != "openrouter" {
		t.Errorf("expected openrouter, got %s", recent[0].Provider)
	}
}

func TestQuery(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	now := time.Now()
	for i := 0; i < 10; i++ {
		store.Record(TurnRecord{
			Timestamp:  now.Add(-time.Duration(i) * time.Minute),
			DurationMs: 1000 + int64(i*100),
			Provider:   "openrouter",
			Model:      "moonshotai/kimi-k2.6",
			Agent:      "soul",
			SessionKey: "telegram:123",
			AccTotalTokens: 100,
		})
	}
	store.Record(TurnRecord{
		Timestamp:  now.Add(-30 * time.Minute),
		DurationMs: 5000,
		Provider:   "deepseek",
		Model:      "deepseek-v4-flash",
		Agent:      "fallout",
		SessionKey: "discord:456",
		AccTotalTokens: 300,
		Error:      true,
	})

	summary := Query(store, Window1H)
	if summary.TotalTurns != 11 {
		t.Fatalf("expected 11 turns, got %d", summary.TotalTurns)
	}
	if len(summary.ByProvider) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(summary.ByProvider))
	}
	if summary.ByProvider["deepseek"].Turns != 1 {
		t.Errorf("expected 1 deepseek turn, got %d", summary.ByProvider["deepseek"].Turns)
	}
	if summary.ErrorRate == "" || summary.ErrorRate == "0.0%" {
		t.Errorf("expected non-zero error rate, got %q", summary.ErrorRate)
	}
}

// TestCacheRateNeverExceeds100MixedProviders is a regression test for the
// >100% cacheHitRate bug: a session mixing a cache-reliable provider with a
// cache-unreliable one (openai-oauth) must not let the unreliable provider's
// cached tokens inflate the ratio past 100%. Numerator (cached) and
// denominator (eligible) must both exclude unreliable providers.
func TestCacheRateNeverExceeds100MixedProviders(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	now := time.Now()

	// Reliable provider: modest cache.
	store.Record(TurnRecord{
		Timestamp: now, Provider: "deepseek", Model: "deepseek-v4-pro",
		SessionKey: "discord:mix", AccPromptTokens: 1000, AccCachedTokens: 800,
	})
	// Unreliable provider in the SAME session: large cached, no eligible.
	store.Record(TurnRecord{
		Timestamp: now, Provider: "openai-oauth", Model: "gpt-5.5",
		SessionKey: "discord:mix", AccPromptTokens: 10000, AccCachedTokens: 9000,
	})

	summary := Query(store, Window1H)
	ss := summary.BySession["discord:mix"]
	if ss == nil {
		t.Fatal("session discord:mix missing from summary")
	}
	// Only the reliable turn contributes to cache stats: 800 / 1000 = 80.0%.
	if ss.CacheHitRate != "80.0%" {
		t.Errorf("cacheHitRate = %q, want 80.0%% (reliable-only); unreliable cached must not inflate it", ss.CacheHitRate)
	}
	if ss.CachedTokens != 800 {
		t.Errorf("CachedTokens = %d, want 800 (reliable-only)", ss.CachedTokens)
	}
}

func TestRotate(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	now := time.Now()
	// Old record (10 days ago)
	store.Record(TurnRecord{
		Timestamp: now.AddDate(0, 0, -10),
		Provider:  "old",
	})
	// Recent record
	store.Record(TurnRecord{
		Timestamp: now,
		Provider:  "recent",
	})

	store.Rotate()

	records := store.Load(time.Time{})
	if len(records) != 1 {
		t.Fatalf("expected 1 record after rotation, got %d", len(records))
	}
	if records[0].Provider != "recent" {
		t.Errorf("expected recent record, got %s", records[0].Provider)
	}

	// Verify file exists
	if _, err := os.Stat(store.filePath()); err != nil {
		t.Errorf("metrics file should exist after rotation: %v", err)
	}
}
