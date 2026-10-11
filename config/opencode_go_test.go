package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenCodeGoCredentials(t *testing.T) {
	t.Setenv("OPENCODE_GO_API_KEY", "")
	t.Setenv("OPENCODE_GO_API_BASE", "")
	cfg := DefaultConfig()
	cfg.SetProvider("opencode-go")
	if _, err := cfg.GetAPIKey(); err == nil {
		t.Fatal("missing key did not report an error")
	}
	cfg.SetProviderAPIKey("config-key")
	cfg.SetProviderAPIBase("https://config.example/v1")
	if cfg.EnsureProviderConfigFor("opencode-go") != cfg.Providers.GetProviderConfig("opencode-go") {
		t.Fatal("provider config accessor allocated a second credential record")
	}
	if key, err := cfg.GetAPIKey(); err != nil || key != "config-key" {
		t.Fatalf("config key = %q, %v", key, err)
	}
	if base := cfg.GetAPIBase(); base != "https://config.example/v1" {
		t.Fatalf("config base = %q", base)
	}
	t.Setenv("OPENCODE_GO_API_KEY", " env-key ")
	t.Setenv("OPENCODE_GO_API_BASE", " https://env.example/v1 ")
	if key, err := cfg.GetAPIKey(); err != nil || key != "env-key" {
		t.Fatalf("environment key = %q, %v", key, err)
	}
	if base := cfg.GetAPIBase(); base != "https://env.example/v1" {
		t.Fatalf("environment base = %q", base)
	}
}

func TestOpenCodeGoConfigRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetProvider("opencode-go")
	cfg.SetProviderAPIKey("go-key")
	cfg.SetProviderAPIBase("https://custom.example/v1")
	for name, codec := range map[string]struct {
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{
		"yaml": {yaml.Marshal, yaml.Unmarshal},
		"json": {json.Marshal, json.Unmarshal},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := codec.marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "opencodeGo") {
				t.Fatal("missing canonical opencodeGo field")
			}
			var loaded Config
			if err := codec.unmarshal(data, &loaded); err != nil {
				t.Fatal(err)
			}
			pc := loaded.Providers.GetProviderConfig("opencode-go")
			if loaded.GetProvider() != "opencode-go" || pc == nil || *pc != *cfg.Providers.OpenCodeGo {
				t.Fatalf("provider or credentials lost during %s roundtrip", name)
			}
		})
	}
}
