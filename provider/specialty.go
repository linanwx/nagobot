package provider

import (
	"sort"
	"strings"
)

// explicitSpecialtyModels restricts certain specialties to a hand-picked set of
// allowed "provider/model" entries. The FIRST slash splits provider from model,
// because model ids may themselves contain slashes (e.g. the openrouter id
// "z-ai/glm-5.2"). A specialty absent here (and not a capability specialty below)
// is unrestricted: onboard offers every model the chosen provider supports.
//
// Currently empty. Its only member was "fast", which pinned the pre-think agent
// to a DeepSeek "-instant" alias; pre-think no longer calls a model at all, so
// that restriction died with it. "fast" itself is live again — quote-summary
// declares it — but deliberately UNrestricted this time: the whitelist existed
// because pre-think blocked every user turn and could not tolerate a reasoning
// model, whereas a quote is one bounded off-path call. Pinning it to one
// provider's one model would leave every non-DeepSeek deployment unable to
// choose anything. The mechanism stays as the hook for the next specialty that
// genuinely needs a hand-picked list.
var explicitSpecialtyModels = map[string][]string{}

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
// model.
//
// Also empty. "fast" was the only member, back when it gated pre-think and an
// absent rule meant "run pre-think without a model". It must NOT be re-added
// now that quote-summary owns the specialty: optional means an absent rule
// DISABLES the feature, so a deployment that skipped the prompt would lose the
// quote button outright. Left non-optional, an absent `fast` rule simply
// cascades to the default model — a slower quote, not a broken one.
var optionalSpecialties = map[string]bool{}

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
