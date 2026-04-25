package config

import cronpkg "github.com/linanwx/nagobot/cron"

const (
	defaultProvider            = "deepseek"
	defaultModelType           = "deepseek-v4-flash"
	defaultMaxTokens           = 8192
	defaultTemperature         = 1.0
	defaultContextWindowTokens = 300000
	defaultWebAddr             = "127.0.0.1:18080"
	defaultSkillHubURL         = "https://clawhub.ai"
)

// defaultCronSeeds returns the built-in cron jobs that are always force-merged into config.
func defaultCronSeeds() []cronpkg.Job {
	return []cronpkg.Job{
		{
			ID:    "tidyup",
			Expr:  "0 4 * * *",
			Task:  `You must call use_skill("tidyup-dispatcher") and follow its instructions. use_skill function can not skip.`,
			Agent: "tidyup",
		},
		{
			ID:    "session-summary",
			Expr:  "0 0 * * *",
			Task:  `You must call use_skill("session-summary-dispatcher") and follow its instructions. use_skill function can not skip.`,
			Agent: "session-summary",
		},
		{
			ID:    "memory-summary",
			Expr:  "0 0 * * *",
			Task:  `You must call use_skill("memory-summary-dispatcher") and follow its instructions. use_skill function can not skip.`,
			Agent: "memory-summary",
		},
		{
			ID:    "world-knowledge",
			Expr:  "0 0 * * *",
			Task:  `You must call use_skill("world-knowledge-updater") and follow its instructions. use_skill function can not skip.`,
			Agent: "search",
		},
	}
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	logDefaults := defaultLoggingConfig()
	return &Config{
		Thread: ThreadConfig{
			Provider:            defaultProvider,
			ModelType:           defaultModelType,
			MaxTokens:           defaultMaxTokens,
			Temperature:         defaultTemperature,
			ContextWindowTokens: defaultContextWindowTokens,
		},
		Providers: ProvidersConfig{
			DeepSeek: &ProviderConfig{
				APIKey: "",
			},
		},
		Channels: &ChannelsConfig{
			Telegram: &TelegramChannelConfig{
				Token:      "",
				AllowedIDs: []int64{},
			},
			Web: &WebChannelConfig{
				Addr: defaultWebAddr,
			},
		},
		Logging: logDefaults,
		Cron:    defaultCronSeeds(),
	}
}

func defaultLoggingConfig() LoggingConfig {
	enabled := true
	return LoggingConfig{
		Enabled: &enabled,
		Level:   "info",
		Stdout:  true,
		File:    "logs/nagobot.log",
	}
}

// applyDefaults fills in missing fields with sensible defaults.
// Returns true if any field was actually modified (caller should persist).
func (c *Config) applyDefaults() bool {
	changed := false

	if c.Thread.Provider == "" {
		c.Thread.Provider = defaultProvider
		changed = true
	}
	if c.Thread.ModelType == "" {
		c.Thread.ModelType = defaultModelType
		changed = true
	}
	if c.Thread.MaxTokens <= 0 {
		c.Thread.MaxTokens = defaultMaxTokens
		changed = true
	}
	if c.Thread.Temperature == 0 {
		c.Thread.Temperature = defaultTemperature
		changed = true
	}
	if c.Thread.ContextWindowTokens <= 0 {
		c.Thread.ContextWindowTokens = defaultContextWindowTokens
		changed = true
	}
	if c.Channels == nil {
		c.Channels = &ChannelsConfig{}
		changed = true
	}
	if c.Channels.Telegram != nil && c.Channels.Telegram.AllowedIDs == nil {
		c.Channels.Telegram.AllowedIDs = []int64{}
		changed = true
	}
	if c.Channels.Web != nil && c.Channels.Web.Addr == "" {
		c.Channels.Web.Addr = defaultWebAddr
		changed = true
	}

	if c.SkillHub.URL == "" {
		c.SkillHub.URL = defaultSkillHubURL
		changed = true
	}

	// Merge default cron seeds by ID.
	// Default seeds always override user config (forced), and missing ones are appended.
	for _, seed := range defaultCronSeeds() {
		found := false
		for i, j := range c.Cron {
			if j.ID == seed.ID {
				found = true
				if !cronJobEqual(j, seed) {
					c.Cron[i] = seed
					changed = true
				}
				break
			}
		}
		if !found {
			c.Cron = append(c.Cron, seed)
			changed = true
		}
	}

	def := defaultLoggingConfig()
	if c.Logging == (LoggingConfig{}) {
		c.Logging = def
		changed = true
		return changed
	}

	hasAny := c.Logging.Level != "" || c.Logging.File != "" || c.Logging.Stdout
	if c.Logging.Enabled == nil && hasAny {
		enabled := true
		c.Logging.Enabled = &enabled
		changed = true
	}
	if c.Logging.Level == "" {
		c.Logging.Level = def.Level
		changed = true
	}
	if c.Logging.File == "" {
		c.Logging.File = def.File
		changed = true
	}
	if !c.Logging.Stdout && c.Logging.File == "" {
		c.Logging.Stdout = def.Stdout
		changed = true
	}
	if c.Logging.Enabled == nil {
		c.Logging.Enabled = def.Enabled
		changed = true
	}
	return changed
}

// cronJobEqual compares the config-relevant fields of two cron jobs.
func cronJobEqual(a, b cronpkg.Job) bool {
	return a.ID == b.ID &&
		a.Kind == b.Kind &&
		a.Expr == b.Expr &&
		a.Task == b.Task &&
		a.Agent == b.Agent &&
		a.WakeSession == b.WakeSession &&
		a.Silent == b.Silent &&
		a.DirectWake == b.DirectWake
}
