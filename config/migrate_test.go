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
			ModelType: "moonshotai/kimi-k2.6",
			Models: []ModelRule{
				{Type: ModelRuleSpecialty, Name: "chat", Provider: "deepseek", ModelType: "deepseek-chat"},
				{Type: ModelRuleSpecialty, Name: "reason", Provider: "deepseek", ModelType: "deepseek-reasoner"},
				{Type: ModelRuleSpecialty, Name: "untouch", Provider: "openrouter", ModelType: "deepseek-chat"}, // not under deepseek provider: leave alone
				{Type: ModelRuleSpecialty, Name: "current", Provider: "deepseek", ModelType: "deepseek-v4-pro"}, // already V4: leave alone
			},
		},
	}

	if !cfg.migrateLegacyModelNames() {
		t.Fatalf("expected migration to report changes")
	}

	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "chat").ModelType; got != "deepseek-v4-flash" {
		t.Errorf("chat specialty not migrated: got %q", got)
	}
	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "reason").ModelType; got != "deepseek-v4-flash" {
		t.Errorf("reason specialty not migrated: got %q", got)
	}
	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "untouch").ModelType; got != "deepseek-chat" {
		t.Errorf("non-deepseek provider route should be preserved, got %q", got)
	}
	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "current").ModelType; got != "deepseek-v4-pro" {
		t.Errorf("V4 name should be preserved, got %q", got)
	}

	// Thread-level fields under openrouter provider shouldn't be touched.
	if cfg.Thread.ModelType != "moonshotai/kimi-k2.6" {
		t.Errorf("non-deepseek thread modelType was rewritten: %q", cfg.Thread.ModelType)
	}
}

func TestMigrateLegacyModelNames_KimiK26(t *testing.T) {
	cfg := &Config{
		Thread: ThreadConfig{
			Provider:  "openrouter",
			ModelType: "moonshotai/kimi-k2.5",
			ModelName: "moonshotai/kimi-k2.5",
			Models: []ModelRule{
				{Type: ModelRuleSpecialty, Name: "chat", Provider: "openrouter", ModelType: "moonshotai/kimi-k2.5"},
				{Type: ModelRuleSpecialty, Name: "cn", Provider: "moonshot-cn", ModelType: "kimi-k2.5"},
				{Type: ModelRuleSpecialty, Name: "global", Provider: "moonshot-global", ModelType: "kimi-k2.5"},
			},
		},
	}

	if !cfg.migrateLegacyModelNames() {
		t.Fatalf("expected migration to report changes")
	}
	if cfg.Thread.ModelType != "moonshotai/kimi-k2.6" {
		t.Errorf("thread ModelType = %q, want moonshotai/kimi-k2.6", cfg.Thread.ModelType)
	}
	if cfg.Thread.ModelName != "moonshotai/kimi-k2.6" {
		t.Errorf("thread ModelName = %q, want moonshotai/kimi-k2.6", cfg.Thread.ModelName)
	}
	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "chat").ModelType; got != "moonshotai/kimi-k2.6" {
		t.Errorf("openrouter rule = %q, want moonshotai/kimi-k2.6", got)
	}
	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "cn").ModelType; got != "kimi-k2.6" {
		t.Errorf("moonshot-cn rule = %q, want kimi-k2.6", got)
	}
	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "global").ModelType; got != "kimi-k2.6" {
		t.Errorf("moonshot-global rule = %q, want kimi-k2.6", got)
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
