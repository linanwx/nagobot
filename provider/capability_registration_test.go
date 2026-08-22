package provider

import (
	"slices"
	"testing"
)

// RegisterProvider trusts VisionModels/AudioModels/PDFModels blindly: it writes
// a "provider:model" capability key without ever checking the id against
// Models. Both directions of a mistake are silent —
//
//   - an id listed in Models but forgotten in VisionModels is a model that
//     drops every image, and the only symptom is the LLM saying it cannot see;
//   - a typo in VisionModels registers the capability under an id nothing can
//     select, so the real model stays blind.
//
// Neither produces an error at startup, so the sweep is the only guard.
func TestCapabilityListsOnlyNameRegisteredModels(t *testing.T) {
	for provider, reg := range providerRegistry {
		check := func(kind string, ids []string) {
			for _, id := range ids {
				if !slices.Contains(reg.Models, id) {
					t.Errorf("%s: %s lists %q, which is not in Models — the capability is registered under an id nobody can select",
						provider, kind, id)
				}
			}
		}
		check("VisionModels", reg.VisionModels)
		check("AudioModels", reg.AudioModels)
		check("PDFModels", reg.PDFModels)

		for id := range reg.ContextWindows {
			if !slices.Contains(reg.Models, id) {
				t.Errorf("%s: ContextWindows has %q, which is not in Models", provider, id)
			}
		}
		// A registered model with no window falls back to a default that is
		// almost certainly wrong for it, and compression is driven by that
		// number.
		for _, id := range reg.Models {
			if _, ok := reg.ContextWindows[id]; !ok {
				t.Errorf("%s: model %q has no context window", provider, id)
			}
		}
	}
}

// The DeepSeek vision model is reachable by two routes and they must agree:
// same model, same ceiling, both able to see. A route registered in Models but
// missing from VisionModels would accept the model and silently drop images.
func TestDeepSeekVisionIsConsistentAcrossBothRoutes(t *testing.T) {
	routes := map[string]string{
		"deepseek":   dsVisionModel,
		"openrouter": "deepseek/" + dsVisionModel,
	}
	windows := map[string]int{}
	for provider, model := range routes {
		if !slices.Contains(providerModelTypes[provider], model) {
			t.Errorf("%s: %q is not registered", provider, model)
			continue
		}
		if !SupportsVision(provider, model) {
			t.Errorf("%s: %q is registered but cannot see", provider, model)
		}
		windows[provider] = providerModelContextWindows[provider+":"+model]
	}
	if len(windows) == 2 && windows["deepseek"] != windows["openrouter"] {
		t.Errorf("the two routes disagree about one model's window: deepseek=%d openrouter=%d",
			windows["deepseek"], windows["openrouter"])
	}
}
