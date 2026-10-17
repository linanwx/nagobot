package cmd

import (
	"errors"
	"testing"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/tools"
)

func TestDeepSeekToolsKeyHotReload(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	cfg := &config.Config{}
	cfg.Thread.Provider = "opencode-go"
	cfg.Providers.OpenCodeGo = &config.ProviderConfig{APIKey: "wrong-provider"}
	var loadErr error
	keyFn := deepSeekToolsKeyFn(func() (*config.Config, error) { return cfg, loadErr })
	search := &tools.DeepSeekSearchProvider{KeyFn: keyFn}
	fetch := &tools.DeepSeekFetchProvider{KeyFn: keyFn}
	if search.Available() || fetch.Available() {
		t.Fatal("must not reuse OpenCode Go key")
	}
	cfg.Providers.DeepSeek = &config.ProviderConfig{APIKey: " official "}
	if keyFn() != "official" || !search.Available() || !fetch.Available() {
		t.Fatal("new native key not visible")
	}
	t.Setenv("DEEPSEEK_API_KEY", " environment ")
	if keyFn() != "environment" {
		t.Fatal("environment must take priority")
	}
	loadErr = errors.New("config unavailable")
	if keyFn() != "environment" {
		t.Fatal("environment key should work without config")
	}
	t.Setenv("DEEPSEEK_API_KEY", "")
	if search.Available() || fetch.Available() {
		t.Fatal("failed config load must not keep stale key")
	}
	loadErr = nil
	cfg.Providers.DeepSeek.APIKey = ""
	if search.Available() || fetch.Available() {
		t.Fatal("removed key still available")
	}
}
