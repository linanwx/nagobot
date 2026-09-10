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
	if cfg.Thread.ModelType != "deepseek-flash" {
		t.Errorf("ModelType not migrated: got %q", cfg.Thread.ModelType)
	}
	if cfg.Thread.ModelName != "deepseek-flash" {
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

	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "chat").ModelType; got != "deepseek-flash" {
		t.Errorf("chat specialty not migrated: got %q", got)
	}
	if got := FindModelRule(cfg.Thread.Models, ModelRuleSpecialty, "reason").ModelType; got != "deepseek-flash" {
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
			ModelType: "deepseek-flash",
		},
	}
	if cfg.migrateLegacyModelNames() {
		t.Errorf("current-model config reported changes")
	}
}

// The retired V4-Flash names are deliberately NOT migrated, and that omission
// is a decision, not an oversight — so it is pinned.
//
// DeepSeek still routes deepseek-v4-flash and deepseek-v4-flash-vision-exp to
// V4.1-Flash, with no announced end date, which is precisely why a silent
// rewrite here would be wrong: it would hide that a deployment is pinned to an
// alias that can stop resolving at any time. The provider registry drops the
// ids instead (TestRetiredModelsAreNotRegistered), so such a config fails loudly
// at load and is corrected by hand.
func TestMigrateLeavesRetiredFlashNamesToFailLoudly(t *testing.T) {
	for _, name := range []string{"deepseek-v4-flash", "deepseek-v4-flash-instant", "deepseek-v4-flash-vision-exp"} {
		cfg := &Config{
			Thread: ThreadConfig{
				Provider:  "deepseek",
				ModelType: name,
				Models: []ModelRule{
					{Type: ModelRuleSpecialty, Name: "chat", Provider: "deepseek", ModelType: name},
				},
			},
		}
		if cfg.migrateLegacyModelNames() {
			t.Errorf("%s was silently rewritten; it must reach the registry and fail there", name)
		}
		if cfg.Thread.ModelType != name {
			t.Errorf("%s rewritten to %q", name, cfg.Thread.ModelType)
		}
	}
}
