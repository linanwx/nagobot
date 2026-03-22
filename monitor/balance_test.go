package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadBalance(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "system", "balance-cache.json")

	entries := []BalanceInfo{
		{
			Provider:  "openrouter",
			Available: true,
			Balances:  []BalanceEntry{{Currency: "USD", Balance: 12.34, Detail: "credits: 20, usage: 7.66"}},
		},
		{
			Provider: "deepseek",
			Error:    "not configured",
		},
	}

	// Save.
	if err := SaveBalance(cachePath, entries); err != nil {
		t.Fatalf("SaveBalance: %v", err)
	}

	// Load.
	loaded, updatedAt, err := LoadBalance(cachePath)
	if err != nil {
		t.Fatalf("LoadBalance: %v", err)
	}
	if time.Since(updatedAt) > 5*time.Second {
		t.Errorf("updatedAt too old: %v", updatedAt)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}
	if loaded[0].Provider != "openrouter" {
		t.Errorf("expected openrouter, got %s", loaded[0].Provider)
	}
	if loaded[0].Balances[0].Balance != 12.34 {
		t.Errorf("expected balance 12.34, got %f", loaded[0].Balances[0].Balance)
	}
	if loaded[1].Error != "not configured" {
		t.Errorf("expected 'not configured' error, got %q", loaded[1].Error)
	}
}

func TestLoadBalanceMissing(t *testing.T) {
	_, _, err := LoadBalance(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadBalanceInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(path, []byte("not json"), 0644)
	_, _, err := LoadBalance(path)
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}
