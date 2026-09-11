package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestOpenAIQuotaResolvesCredentialsPerCall pins the property that made the
// daily health check cry wolf: the daemon builds its checkers once at startup
// and polls for the life of the process, so a credential captured at
// construction time is a credential that dies the first time OAuth rotates.
// Availability must track the CURRENT token, not the one that existed when the
// struct was built.
func TestOpenAIQuotaResolvesCredentialsPerCall(t *testing.T) {
	token := "tok-before-rotation"
	calls := 0
	q := &OpenAIQuota{ConfiguredFn: func() bool {
		calls++
		return token != ""
	}}

	if !q.Available() {
		t.Fatal("expected the checker to be available with a token configured")
	}

	// The inference path refreshes and rewrites config; the checker must see it.
	token = "tok-after-rotation"
	if !q.Available() {
		t.Fatal("expected the checker to stay available across a token rotation")
	}
	if calls < 2 {
		t.Fatalf("credentials resolved %d time(s); a snapshot would resolve once", calls)
	}

	token = ""
	if q.Available() {
		t.Fatal("expected the checker to report unavailable once the token is gone")
	}
}

// Available gates every five-minute poll, so it must answer from stored config
// alone. CredsFn may refresh an expired token over the network, and calling it
// from here would put that round trip on the gate of every checker sweep.
func TestOpenAIQuotaAvailableDoesNotResolveCredentials(t *testing.T) {
	q := &OpenAIQuota{
		ConfiguredFn: func() bool { return true },
		CredsFn: func() (string, string, error) {
			t.Fatal("Available resolved credentials; that path may hit the network")
			return "", "", nil
		},
	}
	if !q.Available() {
		t.Fatal("expected the checker to be available")
	}
}

// A checker with no OAuth configured must not put "Bearer " on the wire just to
// be told 401 and then report that as a credential problem.
func TestOpenAIQuotaWithoutTokenSkipsTheRequest(t *testing.T) {
	for name, q := range map[string]*OpenAIQuota{
		"nil CredsFn": {},
		"empty token": {CredsFn: func() (string, string, error) { return "", "", nil }},
	} {
		info, err := q.Check(context.Background())
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if info.Error == "" {
			t.Fatalf("%s: expected an error describing the missing token", name)
		}
		if strings.Contains(info.Error, "HTTP") {
			t.Fatalf("%s: request went out anyway: %q", name, info.Error)
		}
	}
}

// A credential that exists but cannot be renewed is a different operator
// problem from one that was never configured, and the two must not share a
// message: "no OAuth token configured" sends whoever reads it to `auth login`
// for a token that is sitting in config, which is exactly the wrong-remedy
// alert this checker's comment already warns about. It must also not reach the
// wire — a token we know is dead buys nothing but a 401.
func TestOpenAIQuotaReportsUnrenewableCredentialDistinctly(t *testing.T) {
	q := &OpenAIQuota{
		ConfiguredFn: func() bool { return true },
		CredsFn: func() (string, string, error) {
			return "", "", errors.New("OAuth token expired 2026-09-04 23:00 UTC (5d21h ago) and could not be refreshed — re-authenticate with: nagobot auth login openai")
		},
	}

	info, err := q.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(info.Error, "HTTP") {
		t.Fatalf("request went out with a credential known to be dead: %q", info.Error)
	}
	if strings.Contains(info.Error, "no OAuth token configured") {
		t.Fatalf("an unrenewable credential was reported as an absent one: %q", info.Error)
	}
	if !strings.Contains(info.Error, "could not be refreshed") {
		t.Fatalf("error does not say why the credential is unusable: %q", info.Error)
	}
}
