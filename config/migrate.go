package config

import "github.com/linanwx/nagobot/logger"

// legacyDeepSeekModelRename maps retired DeepSeek model IDs to their V4
// successors. DeepSeek themselves route both legacy names to deepseek-v4-flash
// (reasoner → flash thinking, chat → flash non-thinking) until 2026-07-24 UTC,
// after which the old names stop resolving entirely.
var legacyDeepSeekModelRename = map[string]string{
	"deepseek-reasoner": "deepseek-v4-flash",
	"deepseek-chat":     "deepseek-v4-flash",
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
