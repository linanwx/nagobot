package thread

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
)

// writeAgent writes a minimal agent template declaring the given specialties.
func writeAgent(t *testing.T, dir, name string, specialties string) {
	t.Helper()
	fm := "---\nname: " + name + "\nspecialty: " + specialties + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolvedModelConfig_Precedence(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAgent(t, dir, "writer", "[pdf, toolcall]")              // multi-specialty
	writeAgent(t, dir, "implicit", "[deepseek/deepseek-v4-pro]") // legacy implicit-route form
	reg := agent.NewRegistry(ws)

	newThread := func(agentName, sessionKey string, rules []config.ModelRule) *Thread {
		return &Thread{
			Agent:      &agent.Agent{Name: agentName},
			sessionKey: sessionKey,
			mgr:        NewManager(&ThreadConfig{Agents: reg, Models: rules}),
		}
	}
	rule := func(typ, name, prov, model string) config.ModelRule {
		return config.ModelRule{Type: typ, Name: name, Provider: prov, ModelType: model}
	}

	t.Run("session beats agent beats specialty", func(t *testing.T) {
		rules := []config.ModelRule{
			rule(config.ModelRuleSpecialty, "pdf", "p-spec", "m-spec"),
			rule(config.ModelRuleAgent, "writer", "p-agent", "m-agent"),
			rule(config.ModelRuleSession, "sess-1", "p-sess", "m-sess"),
		}
		mc := newThread("writer", "sess-1", rules).resolvedModelConfig()
		if mc == nil || mc.Provider != "p-sess" || mc.ModelType != "m-sess" {
			t.Fatalf("session rule should win, got %+v", mc)
		}
	})

	t.Run("agent beats specialty", func(t *testing.T) {
		rules := []config.ModelRule{
			rule(config.ModelRuleSpecialty, "pdf", "p-spec", "m-spec"),
			rule(config.ModelRuleAgent, "writer", "p-agent", "m-agent"),
		}
		mc := newThread("writer", "sess-1", rules).resolvedModelConfig()
		if mc == nil || mc.Provider != "p-agent" {
			t.Fatalf("agent rule should win, got %+v", mc)
		}
	})

	t.Run("multi-specialty left-to-right: first match wins", func(t *testing.T) {
		// writer = [pdf, toolcall]; both have rules → pdf (first) wins.
		rules := []config.ModelRule{
			rule(config.ModelRuleSpecialty, "toolcall", "p-tc", "m-tc"),
			rule(config.ModelRuleSpecialty, "pdf", "p-pdf", "m-pdf"),
		}
		mc := newThread("writer", "sess-1", rules).resolvedModelConfig()
		if mc == nil || mc.ModelType != "m-pdf" {
			t.Fatalf("pdf (first specialty) should win, got %+v", mc)
		}
	})

	t.Run("multi-specialty falls through to second when first has no rule", func(t *testing.T) {
		// only toolcall has a rule → toolcall wins (pdf skipped).
		rules := []config.ModelRule{rule(config.ModelRuleSpecialty, "toolcall", "p-tc", "m-tc")}
		mc := newThread("writer", "sess-1", rules).resolvedModelConfig()
		if mc == nil || mc.ModelType != "m-tc" {
			t.Fatalf("toolcall should win, got %+v", mc)
		}
	})

	t.Run("no matching rule → nil (default)", func(t *testing.T) {
		rules := []config.ModelRule{rule(config.ModelRuleSpecialty, "chat", "p", "m")}
		if mc := newThread("writer", "sess-1", rules).resolvedModelConfig(); mc != nil {
			t.Fatalf("expected nil (default), got %+v", mc)
		}
	})

	t.Run("implicit provider/model route dropped", func(t *testing.T) {
		// agent specialty "deepseek/deepseek-v4-pro" no longer auto-routes; with no
		// matching specialty rule it falls to default (nil).
		if mc := newThread("implicit", "sess-1", nil).resolvedModelConfig(); mc != nil {
			t.Fatalf("implicit route should be gone, got %+v", mc)
		}
	})

	t.Run("empty rules → nil", func(t *testing.T) {
		if mc := newThread("writer", "sess-1", nil).resolvedModelConfig(); mc != nil {
			t.Fatalf("expected nil for empty rules, got %+v", mc)
		}
	})
}

func TestResolvedModelConfig_SourceSpecialty(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// soul = basic [chat]; heartbeat source → [lowcost].
	fm := "---\nname: soul\nspecialty: [chat]\nsource_specialty:\n  heartbeat: [lowcost]\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "soul.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := agent.NewRegistry(ws)

	newThread := func(sessionKey string, src WakeSource, rules []config.ModelRule) *Thread {
		return &Thread{
			Agent:          &agent.Agent{Name: "soul"},
			sessionKey:     sessionKey,
			lastWakeSource: src,
			mgr:            NewManager(&ThreadConfig{Agents: reg, Models: rules}),
		}
	}
	rule := func(typ, name, prov, model string) config.ModelRule {
		return config.ModelRule{Type: typ, Name: name, Provider: prov, ModelType: model}
	}

	t.Run("heartbeat source: lowcost wins over agent rule", func(t *testing.T) {
		rules := []config.ModelRule{
			rule(config.ModelRuleSpecialty, "lowcost", "p-low", "m-low"),
			rule(config.ModelRuleAgent, "soul", "p-agent", "m-agent"),
		}
		mc := newThread("sess-1", WakeHeartbeat, rules).resolvedModelConfig()
		if mc == nil || mc.ModelType != "m-low" {
			t.Fatalf("lowcost should win for heartbeat source, got %+v", mc)
		}
	})

	t.Run("cascade: lowcost unset → falls to agent rule", func(t *testing.T) {
		rules := []config.ModelRule{rule(config.ModelRuleAgent, "soul", "p-agent", "m-agent")}
		mc := newThread("sess-1", WakeHeartbeat, rules).resolvedModelConfig()
		if mc == nil || mc.ModelType != "m-agent" {
			t.Fatalf("should cascade to agent rule, got %+v", mc)
		}
	})

	t.Run("cascade: lowcost+agent unset → falls to basic specialty", func(t *testing.T) {
		rules := []config.ModelRule{rule(config.ModelRuleSpecialty, "chat", "p-chat", "m-chat")}
		mc := newThread("sess-1", WakeHeartbeat, rules).resolvedModelConfig()
		if mc == nil || mc.ModelType != "m-chat" {
			t.Fatalf("should cascade to basic specialty, got %+v", mc)
		}
	})

	t.Run("cascade: nothing set → nil (default)", func(t *testing.T) {
		if mc := newThread("sess-1", WakeHeartbeat, nil).resolvedModelConfig(); mc != nil {
			t.Fatalf("expected nil (default), got %+v", mc)
		}
	})

	t.Run("non-heartbeat source ignores source_specialty", func(t *testing.T) {
		// lowcost rule exists, but a telegram wake has no source_specialty entry,
		// so the normal chain applies → chat specialty.
		rules := []config.ModelRule{
			rule(config.ModelRuleSpecialty, "lowcost", "p-low", "m-low"),
			rule(config.ModelRuleSpecialty, "chat", "p-chat", "m-chat"),
		}
		mc := newThread("sess-1", WakeTelegram, rules).resolvedModelConfig()
		if mc == nil || mc.ModelType != "m-chat" {
			t.Fatalf("telegram should use chat, not lowcost, got %+v", mc)
		}
	})

	t.Run("session pin beats source_specialty", func(t *testing.T) {
		rules := []config.ModelRule{
			rule(config.ModelRuleSpecialty, "lowcost", "p-low", "m-low"),
			rule(config.ModelRuleSession, "sess-1", "p-sess", "m-sess"),
		}
		mc := newThread("sess-1", WakeHeartbeat, rules).resolvedModelConfig()
		if mc == nil || mc.ModelType != "m-sess" {
			t.Fatalf("session pin should win over source_specialty, got %+v", mc)
		}
	})
}
