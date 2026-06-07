package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFindModelRule(t *testing.T) {
	rules := []ModelRule{
		{Type: ModelRuleSpecialty, Name: "chat", Provider: "deepseek", ModelType: "deepseek-v4-pro"},
		{Type: ModelRuleSession, Name: "cli", Provider: "openrouter", ModelType: "x"},
	}
	if r := FindModelRule(rules, ModelRuleSession, "cli"); r == nil || r.Provider != "openrouter" {
		t.Errorf("session rule not found: %+v", r)
	}
	if r := FindModelRule(rules, ModelRuleSpecialty, "chat"); r == nil || r.ModelType != "deepseek-v4-pro" {
		t.Errorf("specialty rule not found: %+v", r)
	}
	if r := FindModelRule(rules, ModelRuleAgent, "chat"); r != nil {
		t.Errorf("wrong-type match should be nil, got %+v", r)
	}
	if r := FindModelRule(nil, ModelRuleSpecialty, "chat"); r != nil {
		t.Errorf("nil rules should return nil")
	}
}

func TestUpsertModelRule(t *testing.T) {
	var rules []ModelRule
	rules = UpsertModelRule(rules, ModelRuleSpecialty, "chat", "p1", "m1")
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	// Overwrite in place — no duplicate.
	rules = UpsertModelRule(rules, ModelRuleSpecialty, "chat", "p2", "m2")
	if len(rules) != 1 {
		t.Fatalf("upsert created a duplicate: %d rules", len(rules))
	}
	if rules[0].Provider != "p2" || rules[0].ModelType != "m2" {
		t.Errorf("upsert did not overwrite: %+v", rules[0])
	}
	// Different (type,name) appends.
	rules = UpsertModelRule(rules, ModelRuleSession, "chat", "p3", "m3")
	if len(rules) != 2 {
		t.Fatalf("different type should append: %d rules", len(rules))
	}
}

func TestRemoveModelRule(t *testing.T) {
	rules := []ModelRule{
		{Type: ModelRuleSpecialty, Name: "chat", Provider: "p", ModelType: "m"},
		{Type: ModelRuleSession, Name: "cli", Provider: "p", ModelType: "m"},
	}
	rules = RemoveModelRule(rules, ModelRuleSpecialty, "chat")
	if len(rules) != 1 || rules[0].Type != ModelRuleSession {
		t.Fatalf("remove kept wrong rule: %+v", rules)
	}
	// No-op when not found.
	rules = RemoveModelRule(rules, ModelRuleSpecialty, "nope")
	if len(rules) != 1 {
		t.Fatalf("remove of missing rule changed the slice")
	}
}

func TestModelRuleYAMLRoundTrip(t *testing.T) {
	src := `
- type: specialty
  name: chat
  provider: deepseek
  modelType: deepseek-v4-pro
- type: session
  name: discord:123
  provider: openrouter
  modelType: minimax/minimax-m3
`
	var rules []ModelRule
	if err := yaml.Unmarshal([]byte(src), &rules); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rules) != 2 || rules[0].Name != "chat" || rules[1].Type != ModelRuleSession {
		t.Fatalf("parsed rules wrong: %+v", rules)
	}
	out, err := yaml.Marshal(rules)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back []ModelRule
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(back) != 2 || back[1].ModelType != "minimax/minimax-m3" {
		t.Fatalf("round-trip lost data: %+v", back)
	}
}
