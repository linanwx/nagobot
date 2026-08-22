package provider

import "testing"

func TestZhipuThinkingEnabled(t *testing.T) {
	cases := map[string]bool{
		"glm-5.3": true,
		"unknown": false,
	}
	for model, want := range cases {
		if got := zhipuThinkingEnabled(model); got != want {
			t.Errorf("zhipuThinkingEnabled(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestZhipuReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"glm-5.3": "high",
		"unknown": "",
	}
	for model, want := range cases {
		if got := zhipuReasoningEffort(model); got != want {
			t.Errorf("zhipuReasoningEffort(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestZhipuRequestTemperatureForcedWhenThinking(t *testing.T) {
	// glm-5.3 enables thinking, which forces temperature to 1.
	temp, forced := zhipuRequestTemperature("glm-5.3", 0.7)
	if temp != 1 || !forced {
		t.Errorf("zhipuRequestTemperature(glm-5.3, 0.7) = (%v, %v), want (1, true)", temp, forced)
	}
	// Unknown/non-thinking models keep their configured temperature.
	temp, forced = zhipuRequestTemperature("unknown", 0.7)
	if temp != 0.7 || forced {
		t.Errorf("zhipuRequestTemperature(unknown, 0.7) = (%v, %v), want (0.7, false)", temp, forced)
	}
}

func TestZhipuGLM53Registration(t *testing.T) {
	for _, p := range []string{"zhipu-cn", "zhipu-global"} {
		if err := ValidateProviderModelType(p, "glm-5.3"); err != nil {
			t.Errorf("ValidateProviderModelType(%q, glm-5.3) = %v, want nil", p, err)
		}
		if got := ContextWindowForModel(p, "glm-5.3"); got != 1000000 {
			t.Errorf("ContextWindowForModel(%q, glm-5.3) = %d, want 1000000", p, got)
		}
	}
}
