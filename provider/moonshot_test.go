package provider

import "testing"

func TestMoonshotRequestTemperature(t *testing.T) {
	cases := []struct {
		model      string
		configured float64
		want       float64
		forced     bool
	}{
		// K3 fixes sampling server-side: temperature must be omitted (0).
		{"kimi-k3", 0.7, 0, true},
		{"kimi-k3", 1, 0, true},
		{"kimi-k2.6", 0.7, 1, true},
		{"other-model", 0.7, 0.7, false},
	}
	for _, c := range cases {
		got, forced := moonshotRequestTemperature(c.model, c.configured)
		if got != c.want || forced != c.forced {
			t.Errorf("moonshotRequestTemperature(%q, %v) = (%v, %v), want (%v, %v)",
				c.model, c.configured, got, forced, c.want, c.forced)
		}
	}
}

func TestMoonshotReasoningEffort(t *testing.T) {
	if got := moonshotReasoningEffort("kimi-k3"); got != "max" {
		t.Errorf("kimi-k3 effort = %q, want max", got)
	}
	// K2.x must not receive reasoning_effort (they use chat_template_kwargs).
	if got := moonshotReasoningEffort("kimi-k2.6"); got != "" {
		t.Errorf("kimi-k2.6 effort = %q, want empty", got)
	}
}

func TestKimiK3Registered(t *testing.T) {
	for _, prov := range []string{"moonshot-cn", "moonshot-global"} {
		if w := ContextWindowForModel(prov, "kimi-k3"); w != 1048576 {
			t.Errorf("%s kimi-k3 window = %d, want 1048576", prov, w)
		}
		if !SupportsVision(prov, "kimi-k3") {
			t.Errorf("%s kimi-k3 should be vision-capable", prov)
		}
	}
	if !IsSupportedModel("kimi-k3") {
		t.Error("kimi-k3 missing from supported model types")
	}
}
