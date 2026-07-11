package provider

import (
	"slices"
	"testing"
)

func TestSpecialtyRestricted(t *testing.T) {
	cases := map[string]bool{
		"image":    true,  // capability-derived
		"audio":    true,  // capability-derived
		"pdf":      true,  // capability-derived
		"chat":     false, // unrestricted
		"toolcall": false,
		"writing":  false,
		"":         false,
	}
	for specialty, want := range cases {
		if got := SpecialtyRestricted(specialty); got != want {
			t.Errorf("SpecialtyRestricted(%q) = %v, want %v", specialty, got, want)
		}
	}
}

// TestSpecialtyOptional pins the current truth: nothing is optional any more.
//
// "fast" was the only member, and it existed to gate pre-think — absent rule meant
// the feature was off. Pre-think no longer calls a model, so the gate has nothing
// to gate. The mechanism (and onboard's enable/skip flow behind it) is kept as the
// hook for the next such feature; this test is the sentinel that notices when one
// arrives, so the flow gets re-exercised deliberately rather than by accident.
func TestSpecialtyOptional(t *testing.T) {
	for _, s := range []string{"fast", "image", "audio", "pdf", "chat", "toolcall", ""} {
		if SpecialtyOptional(s) {
			t.Errorf("%q is optional, but optionalSpecialties should be empty — "+
				"if a new optional specialty was added, update this test and check onboard's enable/skip flow", s)
		}
	}
}

func TestAllowedModelsForSpecialty_CapabilityDerived(t *testing.T) {
	// image must equal the provider's vision-capable models, no more no less.
	for _, p := range SupportedProviders() {
		allowed, restricted := AllowedModelsForSpecialty("image", p)
		if !restricted {
			t.Fatalf("image should be restricted (provider %s)", p)
		}
		for _, m := range allowed {
			if !SupportsVision(p, m) {
				t.Errorf("image@%s allowed non-vision model %q", p, m)
			}
		}
		// Every vision model of the provider must be present.
		for _, m := range SupportedModelsForProvider(p) {
			if SupportsVision(p, m) && !slices.Contains(allowed, m) {
				t.Errorf("image@%s missing vision model %q", p, m)
			}
		}
	}
}

func TestAllowedModelsForSpecialty_Unrestricted(t *testing.T) {
	models, restricted := AllowedModelsForSpecialty("chat", "deepseek")
	if restricted {
		t.Error("chat should be unrestricted")
	}
	if models != nil {
		t.Errorf("unrestricted specialty should return nil models, got %v", models)
	}
	if providers, restricted := ProvidersForSpecialty("chat"); restricted || providers != nil {
		t.Errorf("ProvidersForSpecialty(chat) = %v (restricted=%v), want nil,false", providers, restricted)
	}
}
