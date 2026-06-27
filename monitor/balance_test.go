package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSiliconFlowBalanceCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/info" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("unexpected auth header %q", got)
		}
		w.Write([]byte(`{"code":20000,"message":"OK","status":true,"data":{"balance":"0.88","chargeBalance":"88.00","totalBalance":"88.88"}}`))
	}))
	defer srv.Close()

	b := &SiliconFlowBalance{Name: "siliconflow-cn", Base: srv.URL, Currency: "CNY", KeyFn: func() string { return "sk-test" }}
	info, err := b.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !info.Available {
		t.Fatalf("expected available, got error %q", info.Error)
	}
	if len(info.Balances) != 1 {
		t.Fatalf("expected 1 balance entry, got %d", len(info.Balances))
	}
	got := info.Balances[0]
	if got.Currency != "CNY" {
		t.Errorf("currency: got %q want CNY", got.Currency)
	}
	if got.Balance != 88.88 {
		t.Errorf("balance: got %v want 88.88", got.Balance)
	}
}

func TestSiliconFlowBalanceCheckErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":50000,"message":"invalid key","data":{}}`))
	}))
	defer srv.Close()

	b := &SiliconFlowBalance{Name: "siliconflow-cn", Base: srv.URL, Currency: "CNY", KeyFn: func() string { return "sk-bad" }}
	info, err := b.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Available {
		t.Fatal("expected not available on non-20000 code")
	}
	if info.Error == "" {
		t.Fatal("expected error message on non-20000 code")
	}
}

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
