package provider

import "testing"

func TestParseModelEffort(t *testing.T) {
	cases := []struct{ in, base, effort string }{
		{"gpt-5.6-sol[low]", "gpt-5.6-sol", "low"},
		{"gpt-5.6-terra[xhigh]", "gpt-5.6-terra", "xhigh"},
		{"gpt-5.6-luna", "gpt-5.6-luna", ""},
		{"gpt-5.5", "gpt-5.5", ""},
	}
	for _, c := range cases {
		base, effort := parseModelEffort(c.in)
		if base != c.base || effort != c.effort {
			t.Errorf("parseModelEffort(%q) = (%q, %q), want (%q, %q)", c.in, base, effort, c.base, c.effort)
		}
	}
}

func TestEffortVariantsRegistered(t *testing.T) {
	for _, m := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		for _, e := range reasoningEfforts {
			variant := m + "[" + e + "]"
			if !IsSupportedModel(variant) {
				t.Errorf("%s not in supported model types", variant)
			}
			if w := ContextWindowForModel("openai-oauth", variant); w != gpt56ContextWindow {
				t.Errorf("openai-oauth %s window = %d, want %d", variant, w, gpt56ContextWindow)
			}
		}
	}
	// Non-5.6 models get no bracket variants.
	if IsSupportedModel("gpt-5.5[low]") {
		t.Error("gpt-5.5[low] must not be registered")
	}
}
