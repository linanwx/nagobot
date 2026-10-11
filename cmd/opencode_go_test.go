package cmd

import (
	"context"
	"testing"

	"github.com/linanwx/nagobot/config"
)

func TestOpenCodeGoSetProviderKey(t *testing.T) {
	config.SetConfigDir(t.TempDir())
	t.Cleanup(func() { config.SetConfigDir("") })
	oldProvider, oldKey, oldBase := provKeyProvider, provKeyAPIKey, provKeyAPIBase
	oldList, oldClear := provKeyList, provKeyClear
	t.Cleanup(func() {
		provKeyProvider, provKeyAPIKey, provKeyAPIBase = oldProvider, oldKey, oldBase
		provKeyList, provKeyClear = oldList, oldClear
	})
	provKeyProvider, provKeyAPIKey, provKeyAPIBase = "opencode-go", " go-key ", " https://custom.example/v1 "
	provKeyList, provKeyClear = false, false
	if err := runSetProviderKey(nil, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pc := cfg.Providers.GetProviderConfig("opencode-go")
	if pc == nil || pc.APIKey != "go-key" || pc.APIBase != "https://custom.example/v1" {
		t.Fatal("set-provider-key did not persist OpenCode Go credentials")
	}
	provKeyClear = true
	if err := runSetProviderKey(nil, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pc = cfg.Providers.GetProviderConfig("opencode-go")
	if pc == nil || pc.APIKey != "" || pc.APIBase != "" {
		t.Fatal("set-provider-key --clear did not remove OpenCode Go credentials")
	}
}

func TestOpenCodeGoBalanceUnavailable(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.EnsureProviderConfigFor("opencode-go").APIKey = "go-key"
	for _, checker := range buildBalanceCheckers(cfg, t.TempDir()) {
		if checker.Provider() != "opencode-go" {
			continue
		}
		if !checker.Available() {
			t.Fatal("configured OpenCode Go checker is not available")
		}
		info, err := checker.Check(context.Background())
		if err != nil || info == nil || info.Available || len(info.Balances) != 0 || info.Error == "" {
			t.Fatalf("balance query did not report unavailable data: %#v, %v", info, err)
		}
		return
	}
	t.Fatal("OpenCode Go balance checker is not wired")
}
