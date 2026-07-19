package thread

import (
	"os"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/embedding"
)

// The pre-think classifiers embed against a remote OpenAI-compatible endpoint,
// resolved from config by fixed precedence: SiliconFlow CN → SiliconFlow
// Global → OpenRouter → off. Presence of an API key IS the selection — there is
// no health probing or failure-based failover; a backend that errors degrades
// that message to its regex verdict, same as a blown budget.
//
// The model is pinned to Qwen3-Embedding-4B everywhere, and the pin is a
// measured decision, not a default:
//
//   - Same weights on both providers, so anchors and margins are calibrated
//     once and the chain order never changes classification behavior.
//   - 4B is the best-behaved serving: SiliconFlow 4B answered 25/25 probes
//     under 2s (p50 165ms from a CN host, p90 242ms); SiliconFlow 8B blew the
//     2s pre-think budget on 9/25 probes (p90 10.6s) — the same tail that
//     poisons OpenRouter's 8B route whenever it lands on the SiliconFlow
//     endpoint. Bigger is not better here; slower is worse.
//   - Quality needs the instruction format (see qwen3Instructed): with it the
//     4B scores 0 misses / 15∕15 held-out on the destructive set, ahead of
//     every other endpoint measured (gemini-embedding-2, pplx-embed,
//     text-embedding-3-large, bge-m3).
const (
	sfEmbedModel = "Qwen/Qwen3-Embedding-4B"
	orEmbedModel = "qwen/qwen3-embedding-4b"
)

type embedCandidate struct {
	name       string
	defaultURL string
	model      string
	envKey     string // env fallback, mirrors config/provider.go's mapping
	envBase    string
	pc         func(*config.Config) *config.ProviderConfig
}

var embedCandidates = []embedCandidate{
	{"siliconflow-cn", "https://api.siliconflow.cn/v1", sfEmbedModel,
		"SILICONFLOW_API_KEY", "SILICONFLOW_API_BASE",
		func(c *config.Config) *config.ProviderConfig { return c.Providers.SiliconflowCN }},
	{"siliconflow-global", "https://api.siliconflow.com/v1", sfEmbedModel,
		"SILICONFLOW_GLOBAL_API_KEY", "SILICONFLOW_GLOBAL_API_BASE",
		func(c *config.Config) *config.ProviderConfig { return c.Providers.SiliconflowGlobal }},
	{"openrouter", "https://openrouter.ai/api/v1", orEmbedModel,
		"OPENROUTER_API_KEY", "OPENROUTER_API_BASE",
		func(c *config.Config) *config.ProviderConfig { return c.Providers.OpenRouter }},
}

// resolveEmbeddingBackend reads config fresh on every call (the same
// hot-reload contract as provider keys: /init changes take effect without a
// restart) and returns the first candidate with a key, or nil for feature-off.
func resolveEmbeddingBackend() *embedding.Backend {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	for _, cand := range embedCandidates {
		pc := cand.pc(cfg)

		key := strings.TrimSpace(os.Getenv(cand.envKey))
		if key == "" && pc != nil {
			key = strings.TrimSpace(pc.APIKey)
		}
		if key == "" {
			continue
		}

		base := strings.TrimSpace(os.Getenv(cand.envBase))
		if base == "" && pc != nil {
			base = strings.TrimSpace(pc.APIBase)
		}
		if base == "" {
			base = cand.defaultURL
		}
		return &embedding.Backend{
			Name:   cand.name,
			URL:    strings.TrimRight(base, "/") + "/embeddings",
			APIKey: key,
			Model:  cand.model,
		}
	}
	return nil
}
