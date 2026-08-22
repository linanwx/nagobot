package thread

import (
	"os"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/embedding"
)

// The pre-think classifiers embed against a remote OpenAI-compatible endpoint.
// There is exactly one: OpenRouter. Presence of an API key IS the selection —
// no health probing, no failover; a backend that errors degrades that message
// to its regex verdict, same as a blown budget.
//
// This used to be a three-rung chain, SiliconFlow CN → SiliconFlow Global →
// OpenRouter. Both SiliconFlow rungs were removed with the provider (2026-08-22,
// account refunded). Note the failure mode a dead first rung would have had:
// key presence is the whole selection rule, so a SiliconFlow key left in a
// config after the credit ran out would keep WINNING the precedence and send
// every embedding call to an endpoint answering 402 — pre-think silently on
// regex for every message, on the one deployment that still had the key.
// Removing the rungs is what makes that unrepresentable.
//
// The model is pinned to Qwen3-Embedding-4B, and the pin is a measured
// decision, not a default:
//
//   - The anchors baked into embedding/anchors.bin were embedded with these
//     weights, and normalizeModelName folds the two spellings
//     ("Qwen/Qwen3-Embedding-4B" and "qwen/qwen3-embedding-4b") to one
//     identity, so the baked table still hits on this route.
//   - 4B is the best-behaved serving measured: the 8B blew the pre-think budget
//     on 9/25 probes (p90 10.6s), including on OpenRouter whenever its route
//     landed on the SiliconFlow endpoint. Bigger is not better here.
//   - Quality needs the instruction format (see qwen3Instructed): with it the
//     4B scores 0 misses / 15∕15 held-out on the destructive set, ahead of
//     every other endpoint measured (gemini-embedding-2, pplx-embed,
//     text-embedding-3-large, bge-m3).
const orEmbedModel = "qwen/qwen3-embedding-4b"

const (
	embedBackendName = "openrouter"
	embedDefaultURL  = "https://openrouter.ai/api/v1"
	embedEnvKey      = "OPENROUTER_API_KEY" // env fallback, mirrors config/provider.go's mapping
	embedEnvBase     = "OPENROUTER_API_BASE"
)

// resolveEmbeddingBackend reads config fresh on every call (the same
// hot-reload contract as provider keys: /init changes take effect without a
// restart) and returns nil for feature-off.
func resolveEmbeddingBackend() *embedding.Backend {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	pc := cfg.Providers.OpenRouter

	key := strings.TrimSpace(os.Getenv(embedEnvKey))
	if key == "" && pc != nil {
		key = strings.TrimSpace(pc.APIKey)
	}
	if key == "" {
		return nil
	}

	base := strings.TrimSpace(os.Getenv(embedEnvBase))
	if base == "" && pc != nil {
		base = strings.TrimSpace(pc.APIBase)
	}
	if base == "" {
		base = embedDefaultURL
	}
	return &embedding.Backend{
		Name:   embedBackendName,
		URL:    strings.TrimRight(base, "/") + "/embeddings",
		APIKey: key,
		Model:  orEmbedModel,
	}
}
