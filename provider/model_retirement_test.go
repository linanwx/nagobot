package provider

import (
	"strings"
	"testing"
)

func TestRetiredModelsAreNotRegistered(t *testing.T) {
	cases := []struct {
		provider string
		model    string
	}{
		{"openrouter", "qwen/qwen3.6-plus:free"},
		{"openrouter", "x-ai/grok-4.1-fast"},
		{"openrouter", "xiaomi/mimo-v2-pro"},
		{"openrouter", "xiaomi/mimo-v2-omni"},
		{"openrouter", "z-ai/glm-5.1"},
		{"openrouter", "z-ai/glm-5-turbo"},
		{"openrouter", "z-ai/glm-5"},
		{"openrouter", "minimax/minimax-m2.5"},
		{"openrouter", "minimax/minimax-m2.7"},
		{"zhipu-cn", "glm-5"},
		{"zhipu-cn", "glm-5.1"},
		{"zhipu-cn", "glm-5-turbo"},
		{"zhipu-global", "glm-5"},
		{"zhipu-global", "glm-5.1"},
		{"zhipu-global", "glm-5-turbo"},
		{"siliconflow-cn", "Pro/zai-org/GLM-5.1"},
		{"siliconflow-global", "zai-org/GLM-5.1"},
		{"minimax-cn", "minimax-m2.5"},
		{"minimax-cn", "minimax-m2.7"},
		{"minimax-global", "minimax-m2.5"},
		{"minimax-global", "minimax-m2.7"},
		{"mimo", "mimo-v2-pro"},
		{"mimo", "mimo-v2-omni"},
		{"mimo", "mimo-v2-flash"},
		{"openai", "gpt-5.3-codex"},
		{"openai", "gpt-5.2-codex"},
		{"openai", "gpt-5.2"},
		{"openai-oauth", "gpt-5.3-codex"},
		{"openai-oauth", "gpt-5.2-codex"},
		{"openai-oauth", "gpt-5.2"},
		{"openrouter", "moonshotai/kimi-k2.5"},
		{"moonshot-cn", "kimi-k2.5"},
		{"moonshot-global", "kimi-k2.5"},
		{"openrouter", "qwen/qwen3.5-35b-a3b"},
		{"openrouter", "qwen/qwen3.5-flash-02-23"},
		{"openrouter", "google/gemini-3-flash-preview"},
		{"gemini", "gemini-3-flash-preview"},
		{"openrouter", "google/gemini-3.5-flash"},
		{"gemini", "gemini-3.5-flash"},
		{"anthropic", "claude-opus-4-6"},
		{"anthropic", "claude-sonnet-4-6"},
		{"openrouter", "anthropic/claude-opus-4.6"},
		{"openrouter", "anthropic/claude-sonnet-4.6"},
		{"zhipu-cn", "glm-5.2"},
		{"zhipu-global", "glm-5.2"},
		{"openrouter", "z-ai/glm-5.2"},
		{"xai", "grok-4.20-0309-reasoning"},
		{"xai", "grok-4.20-0309-non-reasoning"},
		{"xai", "grok-4-1-fast-reasoning"},
		{"xai", "grok-4-1-fast-non-reasoning"},
	}

	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			if err := ValidateProviderModelType(tc.provider, tc.model); err == nil {
				t.Fatalf("ValidateProviderModelType(%q, %q) = nil, want error", tc.provider, tc.model)
			}
			if got := ContextWindowForModel(tc.provider, tc.model); got != 0 {
				t.Fatalf("ContextWindowForModel(%q, %q) = %d, want 0", tc.provider, tc.model, got)
			}
		})
	}
}

func TestKimiK26Registration(t *testing.T) {
	cases := []struct {
		provider string
		model    string
	}{
		{"openrouter", "moonshotai/kimi-k2.6"},
		{"moonshot-cn", "kimi-k2.6"},
		{"moonshot-global", "kimi-k2.6"},
	}

	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			if err := ValidateProviderModelType(tc.provider, tc.model); err != nil {
				t.Fatalf("ValidateProviderModelType(%q, %q) = %v, want nil", tc.provider, tc.model, err)
			}
			if got := ContextWindowForModel(tc.provider, tc.model); got != 262144 {
				t.Fatalf("ContextWindowForModel(%q, %q) = %d, want 262144", tc.provider, tc.model, got)
			}
			if !SupportsVision(tc.provider, tc.model) {
				t.Fatalf("SupportsVision(%q, %q) = false, want true", tc.provider, tc.model)
			}
		})
	}
}

func TestOpenAIGPT56Registration(t *testing.T) {
	cases := []struct {
		provider string
		model    string
	}{
		{"openai", "gpt-5.6-sol"},
		{"openai", "gpt-5.6-terra"},
		{"openai", "gpt-5.6-luna"},
		{"openai-oauth", "gpt-5.6-sol"},
		{"openai-oauth", "gpt-5.6-terra"},
		{"openai-oauth", "gpt-5.6-luna"},
	}

	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			if err := ValidateProviderModelType(tc.provider, tc.model); err != nil {
				t.Fatalf("ValidateProviderModelType(%q, %q) = %v, want nil", tc.provider, tc.model, err)
			}
			// Deliberately 272000, not the model's real 372000 capacity: past
			// 272K input, OpenAI bills the whole gpt-5.6 request at 2x input /
			// 1.5x output. Registering the true capacity would let context grow
			// to 282K before Tier 2 compression ever fired, double-billing every
			// request in the 272K–282K band. See gpt56ContextWindow.
			if got := ContextWindowForModel(tc.provider, tc.model); got != gpt56ContextWindow {
				t.Fatalf("ContextWindowForModel(%q, %q) = %d, want %d (the 2x price break, not the 372000 capacity)",
					tc.provider, tc.model, got, gpt56ContextWindow)
			}
			if !SupportsVision(tc.provider, tc.model) {
				t.Fatalf("SupportsVision(%q, %q) = false, want true", tc.provider, tc.model)
			}
		})
	}
}

// TestGPT56ContextWindowStaysUnderPriceBreak guards the invariant that actually
// matters: Tier 2 compression must fire before input can reach OpenAI's 272K
// higher-usage threshold. The registered window is the only input to that
// calculation, so a well-meaning "restore the real 372K capacity" edit would
// silently reintroduce double billing. This asserts the math, not the constant.
func TestGPT56ContextWindowStaysUnderPriceBreak(t *testing.T) {
	const priceBreak = 272000

	if gpt56ContextWindow > priceBreak {
		t.Fatalf("gpt56ContextWindow = %d exceeds the %d price break: every request past %d is billed 2x input / 1.5x output",
			gpt56ContextWindow, priceBreak, priceBreak)
	}

	// Mirror thread.ComputeContextThresholds (Tier 2 fires at 70% of the
	// window) and thread.contextLoopBudget, the last-resort trim line that is
	// the actual hard bound on request size: min(92% of the window,
	// window − maxTokens − 10K) with the 16,384 maxTokens default. Tier 2
	// firing under the break is the soft guarantee; the trim line staying
	// under it is the structural one — a "restore the real 372K capacity"
	// edit would push the trim line to ~342K and reintroduce double billing
	// even though Tier 2 (at 260K) would still look safe.
	tier2At := gpt56ContextWindow * 70 / 100
	if tier2At >= priceBreak {
		t.Fatalf("Tier 2 would not fire until %d tokens, at or past the %d price break", tier2At, priceBreak)
	}
	trimAt := min(gpt56ContextWindow*92/100, gpt56ContextWindow-16384-10000)
	if trimAt >= priceBreak {
		t.Fatalf("the context budget trim line %d is at or past the %d price break: requests in between are billed 2x", trimAt, priceBreak)
	}
	t.Logf("gpt-5.6 window %d → Tier 2 at %d, trim line at %d, %d tokens of headroom under the %d price break",
		gpt56ContextWindow, tier2At, trimAt, priceBreak-trimAt, priceBreak)
}

// TestGemini37FlashRegistration pins the two routes gemini-3.7-flash is
// reachable by. The openRouterModels assertion is the one that would otherwise
// fail silently: a model listed in the registration but missing from that map
// gets the zero-value meta, so it ships with no reasoning effort and no
// provider.order pin — a working request, routed to whichever upstream
// OpenRouter picks, with thinking off.
func TestGemini37FlashRegistration(t *testing.T) {
	cases := []struct {
		provider string
		model    string
	}{
		{"gemini", "gemini-3.7-flash"},
		{"openrouter", "google/gemini-3.7-flash"},
	}

	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			if err := ValidateProviderModelType(tc.provider, tc.model); err != nil {
				t.Fatalf("ValidateProviderModelType(%q, %q) = %v, want nil", tc.provider, tc.model, err)
			}
			if got := ContextWindowForModel(tc.provider, tc.model); got != 1048576 {
				t.Fatalf("ContextWindowForModel(%q, %q) = %d, want 1048576", tc.provider, tc.model, got)
			}
			if !SupportsVision(tc.provider, tc.model) {
				t.Fatalf("SupportsVision(%q, %q) = false, want true", tc.provider, tc.model)
			}
			if !SupportsAudio(tc.provider, tc.model) {
				t.Fatalf("SupportsAudio(%q, %q) = false, want true", tc.provider, tc.model)
			}
		})
	}

	meta, ok := openRouterModels["google/gemini-3.7-flash"]
	if !ok {
		t.Fatal("google/gemini-3.7-flash missing from openRouterModels — it would ship with no thinking opts and no provider.order")
	}
	if len(meta.ThinkingOpts) == 0 {
		t.Error("google/gemini-3.7-flash has no ThinkingOpts, want reasoning effort set")
	}
	if len(meta.ProviderOrder) == 0 {
		t.Error("google/gemini-3.7-flash has no ProviderOrder, want the google-ai-studio pin")
	}

	// Adding a model must not displace the ones already registered.
	if err := ValidateProviderModelType("gemini", "gemini-3.1-flash-lite"); err != nil {
		t.Errorf("ValidateProviderModelType(gemini, gemini-3.1-flash-lite) = %v, want nil", err)
	}
}

// TestOpenRouterModelOptsHaveNoStaleEntries is retirement hygiene for the one
// map that is not reachable through the registration API. Removing a model
// from RegisterProvider is what the retirement test above checks; the
// per-model request options live in a separate literal, and a leftover entry
// there is invisible — it validates nothing, breaks nothing, and quietly
// documents a routing decision for a model that can no longer be selected.
func TestOpenRouterModelOptsHaveNoStaleEntries(t *testing.T) {
	registered := make(map[string]bool)
	for _, m := range SupportedModelsForProvider("openrouter") {
		registered[m] = true
	}
	for model := range openRouterModels {
		if !registered[model] {
			t.Errorf("openRouterModels has an entry for %q, which openrouter no longer registers — delete it", model)
		}
	}
}

// TestGemini35FlashLiteRegistration covers the second Gemini lite entry. The
// ThinkingOpts assertion is inverted on purpose: every other google entry in
// openRouterModels carries a reasoning effort, and 3.1-flash-lite deliberately
// does not — a lite model is chosen for price, and quietly enabling reasoning
// on it bills for tokens nobody asked for. 3.5-flash-lite follows its sibling,
// not the flash entry above it.
func TestGemini35FlashLiteRegistration(t *testing.T) {
	cases := []struct {
		provider string
		model    string
	}{
		{"gemini", "gemini-3.5-flash-lite"},
		{"openrouter", "google/gemini-3.5-flash-lite"},
	}

	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			if err := ValidateProviderModelType(tc.provider, tc.model); err != nil {
				t.Fatalf("ValidateProviderModelType(%q, %q) = %v, want nil", tc.provider, tc.model, err)
			}
			if got := ContextWindowForModel(tc.provider, tc.model); got != 1048576 {
				t.Fatalf("ContextWindowForModel(%q, %q) = %d, want 1048576", tc.provider, tc.model, got)
			}
			if !SupportsVision(tc.provider, tc.model) {
				t.Fatalf("SupportsVision(%q, %q) = false, want true", tc.provider, tc.model)
			}
			if !SupportsAudio(tc.provider, tc.model) {
				t.Fatalf("SupportsAudio(%q, %q) = false, want true", tc.provider, tc.model)
			}
		})
	}

	meta, ok := openRouterModels["google/gemini-3.5-flash-lite"]
	if !ok {
		t.Fatal("google/gemini-3.5-flash-lite missing from openRouterModels — it would ship with no provider.order pin")
	}
	if len(meta.ProviderOrder) == 0 {
		t.Error("google/gemini-3.5-flash-lite has no ProviderOrder, want the google-ai-studio pin")
	}
	if len(meta.ThinkingOpts) != 0 {
		t.Error("google/gemini-3.5-flash-lite has ThinkingOpts, want none — a lite model is priced for no reasoning, like 3.1-flash-lite")
	}
}

// TestFableModelsAreNeverRegistered enforces a standing project policy: nagobot
// does not support Anthropic's Fable line, on any provider, ever.
//
// It is a sweep rather than a list of ids because the policy is about a FAMILY,
// not about the ids that happen to exist today. `anthropic/claude-fable-5` is
// routable on OpenRouter right now and would otherwise be a one-line addition
// that nothing objects to; a future `claude-fable-6` has to be caught by the
// same test without anyone remembering it exists. Enumerating ids would fail
// exactly once — the first time someone adds the next one.
func TestFableModelsAreNeverRegistered(t *testing.T) {
	for _, providerName := range SupportedProviders() {
		for _, model := range SupportedModelsForProvider(providerName) {
			if strings.Contains(strings.ToLower(model), "fable") {
				t.Errorf("provider %q registers %q — Fable models are deliberately unsupported (see CLAUDE.md); remove it rather than relaxing this test",
					providerName, model)
			}
		}
	}

	// The ids that exist today, asserted through the validator the rest of the
	// system actually calls.
	for _, tc := range []struct{ provider, model string }{
		{"anthropic", "claude-fable-5"},
		{"openrouter", "anthropic/claude-fable-5"},
	} {
		if err := ValidateProviderModelType(tc.provider, tc.model); err == nil {
			t.Errorf("ValidateProviderModelType(%q, %q) = nil, want an error", tc.provider, tc.model)
		}
	}
}

// TestAnthropicThinkingModeMatchesTheModelGeneration pins the split that decides
// the request shape. Getting it wrong is not a degradation, it is a 400: an
// adaptive-only model rejects `budget_tokens` AND every sampling parameter, so
// a model added to the registry without a mode falls to anthropicThinkingOff and
// silently loses thinking, while one wrongly marked budgeted fails every call.
func TestAnthropicThinkingModeMatchesTheModelGeneration(t *testing.T) {
	for _, m := range SupportedModelsForProvider("anthropic") {
		if anthropicThinkingModeFor(m) == anthropicThinkingOff {
			t.Errorf("anthropic model %q has no thinking mode — it would run without thinking", m)
		}
	}
	for _, m := range []string{"claude-opus-5", "claude-sonnet-5"} {
		if got := anthropicThinkingModeFor(m); got != anthropicThinkingAdaptive {
			t.Errorf("anthropicThinkingModeFor(%q) = %v, want adaptive", m, got)
		}
		// Sampling parameters are rejected outright on these models, so the
		// only correct temperature is "none sent" — a zero first return.
		if temp, _ := anthropicRequestTemperature(anthropicThinkingModeFor(m), 0.7); temp != 0 {
			t.Errorf("anthropicRequestTemperature(adaptive, 0.7) = %v, want 0 (send nothing)", temp)
		}
	}
	if got := anthropicThinkingModeFor("claude-haiku-4-5"); got != anthropicThinkingBudgeted {
		t.Errorf("anthropicThinkingModeFor(claude-haiku-4-5) = %v, want budgeted", got)
	}
	if temp, forced := anthropicRequestTemperature(anthropicThinkingBudgeted, 0.7); temp != 1 || !forced {
		t.Errorf("anthropicRequestTemperature(budgeted, 0.7) = (%v, %v), want (1, true)", temp, forced)
	}
}
