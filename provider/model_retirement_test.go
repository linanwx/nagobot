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
		{"openrouter", "minimax/minimax-m2.5"},
		{"openrouter", "minimax/minimax-m2.7"},
		{"zhipu-cn", "glm-5.1"},
		{"zhipu-cn", "glm-5-turbo"},
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
