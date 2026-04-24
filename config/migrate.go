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

	for key, mc := range c.Thread.Models {
		if mc == nil || mc.Provider != "deepseek" {
			continue
		}
		if repl, ok := legacyDeepSeekModelRename[mc.ModelType]; ok {
			logger.Info("config migration: rename thread.models entry", "specialty", key, "from", mc.ModelType, "to", repl)
			mc.ModelType = repl
			changed = true
		}
	}

	return changed
}
