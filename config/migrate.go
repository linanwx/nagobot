package config

import "github.com/linanwx/nagobot/logger"

// legacyDeepSeekModelRename maps retired DeepSeek model IDs to their current
// successor. The target moved to deepseek-flash (V4.1-Flash) on 2026-09-10,
// when deepseek-v4-flash was itself retired — a migration whose destination is
// also dead just relocates the failure.
//
// Deliberately NOT listed here: deepseek-v4-flash and
// deepseek-v4-flash-vision-exp. DeepSeek still routes both to deepseek-flash,
// so a config naming one keeps working against the API — which is exactly why
// it must fail HERE instead. Silently rewriting a live pin would hide that the
// deployment is riding an alias with no announced end date; the registry drops
// the ids (see TestRetiredModelsAreNotRegistered) and each config is updated by
// hand.
var legacyDeepSeekModelRename = map[string]string{
	"deepseek-reasoner": "deepseek-flash",
	"deepseek-chat":     "deepseek-flash",
}

var legacyKimiModelRename = map[string]map[string]string{
	"moonshot-cn": {
		"kimi-k2.5": "kimi-k2.6",
	},
	"moonshot-global": {
		"kimi-k2.5": "kimi-k2.6",
	},
	"openrouter": {
		"moonshotai/kimi-k2.5": "moonshotai/kimi-k2.6",
	},
}

// migrateLegacyModelNames rewrites retired provider-specific model identifiers
// in-place. Returns true when any field was rewritten so the caller can persist.
func (c *Config) migrateLegacyModelNames() bool {
	changed := false

	if c.Thread.Provider == "deepseek" {
		if repl, ok := legacyDeepSeekModelRename[c.Thread.ModelType]; ok {
			logger.Info("config migration: rename thread.modelType", "from", c.Thread.ModelType, "to", repl)
			c.Thread.ModelType = repl
			changed = true
		}
		if repl, ok := legacyDeepSeekModelRename[c.Thread.ModelName]; ok {
			logger.Info("config migration: rename thread.modelName", "from", c.Thread.ModelName, "to", repl)
			c.Thread.ModelName = repl
			changed = true
		}
	}
	if repl, ok := legacyModelReplacement(c.Thread.Provider, c.Thread.ModelType); ok {
		logger.Info("config migration: rename thread.modelType", "from", c.Thread.ModelType, "to", repl)
		c.Thread.ModelType = repl
		changed = true
	}
	if repl, ok := legacyModelReplacement(c.Thread.Provider, c.Thread.ModelName); ok {
		logger.Info("config migration: rename thread.modelName", "from", c.Thread.ModelName, "to", repl)
		c.Thread.ModelName = repl
		changed = true
	}

	for i := range c.Thread.Models {
		r := &c.Thread.Models[i]
		if r.Provider == "deepseek" {
			if repl, ok := legacyDeepSeekModelRename[r.ModelType]; ok {
				logger.Info("config migration: rename thread.models rule", "type", r.Type, "name", r.Name, "from", r.ModelType, "to", repl)
				r.ModelType = repl
				changed = true
			}
			continue
		}
		if repl, ok := legacyModelReplacement(r.Provider, r.ModelType); ok {
			logger.Info("config migration: rename thread.models rule", "type", r.Type, "name", r.Name, "from", r.ModelType, "to", repl)
			r.ModelType = repl
			changed = true
		}
	}

	return changed
}

func legacyModelReplacement(provider, model string) (string, bool) {
	replacements, ok := legacyKimiModelRename[provider]
	if !ok {
		return "", false
	}
	repl, ok := replacements[model]
	return repl, ok
}
