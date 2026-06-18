package provider

import (
	"slices"
	"testing"
)

func TestSpecialtyRestricted(t *testing.T) {
	cases := map[string]bool{
		"fast":     true,  // explicit whitelist
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

func TestSpecialtyOptional(t *testing.T) {
	if !SpecialtyOptional("fast") {
		t.Error("fast should be optional (on/off feature, never defaults)")
	}
	for _, s := range []string{"image", "audio", "pdf", "chat", "toolcall", ""} {
		if SpecialtyOptional(s) {
			t.Errorf("%q should not be optional", s)
		}
	}
}

func TestAllowedModelsForSpecialty_FastExplicit(t *testing.T) {
	// fast is locked to the DeepSeek official-direct instant aliases.
	models, restricted := AllowedModelsForSpecialty("fast", "deepseek")
	if !restricted {
		t.Fatal("fast should be restricted")
	}
	if !slices.Equal(models, []string{"deepseek-v4-flash-instant", "deepseek-v4-pro-instant"}) {
		t.Fatalf("fast@deepseek = %v, want [deepseek-v4-flash-instant deepseek-v4-pro-instant]", models)
	}

	// No other provider satisfies fast.
	if models, _ := AllowedModelsForSpecialty("fast", "openrouter"); len(models) != 0 {
		t.Errorf("fast@openrouter = %v, want empty", models)
	}

	// Only deepseek appears in the provider list.
	providers, restricted := ProvidersForSpecialty("fast")
	if !restricted || !slices.Equal(providers, []string{"deepseek"}) {
		t.Errorf("ProvidersForSpecialty(fast) = %v (restricted=%v), want [deepseek]", providers, restricted)
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
