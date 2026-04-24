package config

import "testing"

func TestMigrateLegacyModelNames_ThreadLevel(t *testing.T) {
	cfg := &Config{
		Thread: ThreadConfig{
			Provider:  "deepseek",
			ModelType: "deepseek-reasoner",
			ModelName: "deepseek-chat",
		},
	}

	if !cfg.migrateLegacyModelNames() {
		t.Fatalf("expected migration to report changes")
	}
	if cfg.Thread.ModelType != "deepseek-v4-flash" {
		t.Errorf("ModelType not migrated: got %q", cfg.Thread.ModelType)
	}
	if cfg.Thread.ModelName != "deepseek-v4-flash" {
		t.Errorf("ModelName not migrated: got %q", cfg.Thread.ModelName)
	}

	// Second call should be a no-op.
	if cfg.migrateLegacyModelNames() {
		t.Errorf("second migration call unexpectedly reported changes")
	}
}

func TestMigrateLegacyModelNames_PerSpecialtyRouting(t *testing.T) {
	cfg := &Config{
		Thread: ThreadConfig{
			Provider:  "openrouter",
			ModelType: "moonshotai/kimi-k2.5",
			Models: map[string]*ModelConfig{
				"chat":     {Provider: "deepseek", ModelType: "deepseek-chat"},
				"reason":   {Provider: "deepseek", ModelType: "deepseek-reasoner"},
				"untouch":  {Provider: "openrouter", ModelType: "deepseek-chat"}, // not under deepseek provider: leave alone
				"current":  {Provider: "deepseek", ModelType: "deepseek-v4-pro"}, // already V4: leave alone
			},
		},
	}

	if !cfg.migrateLegacyModelNames() {
		t.Fatalf("expected migration to report changes")
	}

	if got := cfg.Thread.Models["chat"].ModelType; got != "deepseek-v4-flash" {
		t.Errorf("chat specialty not migrated: got %q", got)
	}
	if got := cfg.Thread.Models["reason"].ModelType; got != "deepseek-v4-flash" {
		t.Errorf("reason specialty not migrated: got %q", got)
	}
	if got := cfg.Thread.Models["untouch"].ModelType; got != "deepseek-chat" {
		t.Errorf("non-deepseek provider route should be preserved, got %q", got)
	}
	if got := cfg.Thread.Models["current"].ModelType; got != "deepseek-v4-pro" {
		t.Errorf("V4 name should be preserved, got %q", got)
	}

	// Thread-level fields under openrouter provider shouldn't be touched.
	if cfg.Thread.ModelType != "moonshotai/kimi-k2.5" {
		t.Errorf("non-deepseek thread modelType was rewritten: %q", cfg.Thread.ModelType)
	}
}

func TestMigrateLegacyModelNames_NoOp(t *testing.T) {
	cfg := &Config{
		Thread: ThreadConfig{
			Provider:  "deepseek",
			ModelType: "deepseek-v4-flash",
		},
	}
	if cfg.migrateLegacyModelNames() {
		t.Errorf("V4-only config reported changes")
	}
}
