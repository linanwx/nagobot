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
		Model:      "qwen/qwen3.5-35b-a3b",
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
		Model:      "deepseek-chat",
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
			Model:      "qwen/qwen3.5-35b-a3b",
			Agent:      "soul",
			SessionKey: "telegram:123",
			AccTotalTokens: 100,
		})
	}
	store.Record(TurnRecord{
		Timestamp:  now.Add(-30 * time.Minute),
		DurationMs: 5000,
		Provider:   "deepseek",
		Model:      "deepseek-chat",
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
	if summary.ErrorRate == 0 {
		t.Error("expected non-zero error rate")
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
