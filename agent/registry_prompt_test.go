package agent

import (
	"strings"
	"testing"

	"github.com/linanwx/nagobot/config"
)

func TestBuildAgentsPrompt_RoutingAndGaps(t *testing.T) {
	defs := []*AgentDef{
		{Name: "soul", Description: "Main persona", Specialties: []string{"chat"}},
		{Name: "imagereader", Description: "Reads images", Specialties: []string{"image"}},
		{Name: "audioreader", Description: "Transcribes audio", Specialties: []string{"audio"}},
		{Name: "helper", Description: "Generic helper"},
	}
	rules := []config.ModelRule{
		{Type: "specialty", Name: "chat", Provider: "deepseek", ModelType: "deepseek-v4-pro"},
		// audio routed to a genuinely audio-capable registered model.
		{Type: "specialty", Name: "audio", Provider: "openrouter", ModelType: "google/gemini-3.1-flash-lite"},
		// image left unrouted → falls to non-vision default → gap.
	}
	out := buildAgentsPrompt(defs, rules, "deepseek", "deepseek-v4-flash", "/ws")

	for _, want := range []string{
		"- soul: Main persona",
		"routing: deepseek/deepseek-v4-pro (via specialty chat)",
		"routing: default (no rule for specialty image)",
		"requires a vision-capable model",
		"set-model --type image --provider <provider> --model <vision-capable model>",
		"/ws/bin/nagobot set-model --list",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Audio agent is routed to a capable model — must NOT be flagged.
	if strings.Contains(out, "audio-capable model>") {
		t.Errorf("audioreader wrongly flagged as gap:\n%s", out)
	}
	// helper has no specialties: plain default, no gap.
	if !strings.Contains(out, "- helper: Generic helper\n  routing: default\n") {
		t.Errorf("helper routing line wrong:\n%s", out)
	}
	// Deterministic output (prompt cache): same input twice, same bytes.
	if out != buildAgentsPrompt(defs, rules, "deepseek", "deepseek-v4-flash", "/ws") {
		t.Error("output not deterministic")
	}
}

func TestResolveAgentRoute_AgentRuleWinsAndCascades(t *testing.T) {
	def := &AgentDef{Name: "imagereader", Specialties: []string{"image"}}
	rules := []config.ModelRule{
		{Type: "agent", Name: "imagereader", Provider: "openai-oauth", ModelType: "gpt-5.6-luna"},
		{Type: "specialty", Name: "image", Provider: "deepseek", ModelType: "deepseek-v4-flash"},
	}
	route := resolveAgentRoute(def, rules, "deepseek", "deepseek-v4-flash")
	if route.Via != "agent rule" || route.Model != "gpt-5.6-luna" {
		t.Fatalf("agent rule should win: %+v", route)
	}
	// gpt-5.6-luna on openai-oauth is vision-capable — no gap.
	if route.Gap != "" {
		t.Fatalf("unexpected gap: %+v", route)
	}
}
