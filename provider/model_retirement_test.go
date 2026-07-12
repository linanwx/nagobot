package provider

import "testing"

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
