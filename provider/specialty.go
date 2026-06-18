package provider

import (
	"sort"
	"strings"
)

// explicitSpecialtyModels restricts certain specialties to a hand-picked set of
// allowed "provider/model" entries. The FIRST slash splits provider from model,
// because model ids may themselves contain slashes (e.g. the openrouter id
// "z-ai/glm-5"). A specialty absent here (and not a capability specialty below)
// is unrestricted: onboard offers every model the chosen provider supports.
//
//	fast — the pre-think agent must use a non-reasoning, high-throughput,
//	officially-direct model for latency and stability. Only DeepSeek's
//	v4-flash "-instant" alias (thinking disabled) qualifies today.
var explicitSpecialtyModels = map[string][]string{
	"fast": {"deepseek/deepseek-v4-flash-instant"},
}

// specialtyCapability maps a capability specialty to its per-(provider, model)
// predicate. These derive their allowed models from the provider registries
// (VisionModels/AudioModels/PDFModels) so there is a single source of truth —
// adding a capable model to a provider automatically makes it eligible here.
func specialtyCapability(specialty string) func(providerName, model string) bool {
	switch specialty {
	case "image":
		return SupportsVision
	case "audio":
		return SupportsAudio
	case "pdf":
		return SupportsPDF
	}
	return nil
}

// optionalSpecialties are on/off feature toggles: when the specialty has no
// model rule the associated feature is DISABLED — it never falls back to the
// default model. onboard offers enable/skip for these instead of forcing a
// model. The runtime already enforces this (e.g. thread.fastModelConfigured
// gates pre-think); this set keeps onboard's UX in sync with that semantic.
var optionalSpecialties = map[string]bool{
	"fast": true, // pre-think — runs only when fast is configured.
}

// SpecialtyOptional reports whether a specialty is an on/off feature toggle
// (absent rule = disabled, never defaulted) rather than a model that must be
// chosen.
func SpecialtyOptional(specialty string) bool {
	return optionalSpecialties[specialty]
}

// SpecialtyRestricted reports whether a specialty carries a model whitelist.
func SpecialtyRestricted(specialty string) bool {
	if _, ok := explicitSpecialtyModels[specialty]; ok {
		return true
	}
	return specialtyCapability(specialty) != nil
}

// AllowedModelsForSpecialty returns the subset of the given provider's models
// allowed for the specialty, in a stable order. restricted=false means the
// specialty imposes no whitelist and the caller should fall back to the
// provider's full model list (SupportedModelsForProvider).
func AllowedModelsForSpecialty(specialty, providerName string) (models []string, restricted bool) {
	if entries, ok := explicitSpecialtyModels[specialty]; ok {
		for _, e := range entries {
			p, m, found := strings.Cut(e, "/")
			if !found || p != providerName {
				continue
			}
			// Defensive: only surface models the provider actually registers.
			if ValidateProviderModelType(providerName, m) == nil {
				models = append(models, m)
			}
		}
		return models, true
	}
	if pred := specialtyCapability(specialty); pred != nil {
		for _, m := range SupportedModelsForProvider(providerName) {
			if pred(providerName, m) {
				models = append(models, m)
			}
		}
		return models, true
	}
	return nil, false
}

// ProvidersForSpecialty returns the providers that offer at least one allowed
// model for the specialty, sorted. restricted=false means the specialty is
// unrestricted and the caller should offer all SupportedProviders.
func ProvidersForSpecialty(specialty string) (providers []string, restricted bool) {
	if !SpecialtyRestricted(specialty) {
		return nil, false
	}
	for _, p := range SupportedProviders() {
		if models, _ := AllowedModelsForSpecialty(specialty, p); len(models) > 0 {
			providers = append(providers, p)
		}
	}
	sort.Strings(providers)
	return providers, true
}
