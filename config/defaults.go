package config

import cronpkg "github.com/linanwx/nagobot/cron"

const (
	defaultProvider            = "deepseek"
	defaultModelType           = "deepseek-reasoner"
	defaultMaxTokens           = 8192
	defaultTemperature         = 0.95
	defaultContextWindowTokens = 128000
	defaultContextWarnRatio    = 0.8
	defaultWebAddr             = "127.0.0.1:8080"
)

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
			ContextWarnRatio:    defaultContextWarnRatio,
		},
		Providers: ProvidersConfig{
			DeepSeek: &ProviderConfig{
				APIKey: "",
			},
		},
		Channels: &ChannelsConfig{
			AdminUserID: "",
			Telegram: &TelegramChannelConfig{
				Token:      "",
				AllowedIDs: []int64{},
			},
			Web: &WebChannelConfig{
				Addr: defaultWebAddr,
			},
		},
		Logging: logDefaults,
		Cron: []cronpkg.Job{
			{
				ID:          "heartbeat",
				Expr:        "*/30 * * * *",
				Task:        "heartbeat",
				Agent:       "heartbeat",
				Silent:      true,
			},
			{
				ID:     "tidyup",
				Expr:   "0 4 * * *",
				Task:   "tidyup",
				Agent:  "tidyup",
				Silent: true,
			},
		},
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

func (c *Config) applyDefaults() {
	if c.Thread.Provider == "" {
		c.Thread.Provider = defaultProvider
	}
	if c.Thread.ModelType == "" {
		c.Thread.ModelType = defaultModelType
	}
	if c.Thread.MaxTokens <= 0 {
		c.Thread.MaxTokens = defaultMaxTokens
	}
	if c.Thread.Temperature == 0 {
		c.Thread.Temperature = defaultTemperature
	}
	if c.Thread.ContextWindowTokens <= 0 {
		c.Thread.ContextWindowTokens = defaultContextWindowTokens
	}
	if c.Thread.ContextWarnRatio <= 0 || c.Thread.ContextWarnRatio >= 1 {
		c.Thread.ContextWarnRatio = defaultContextWarnRatio
	}

	if c.Channels == nil {
		c.Channels = &ChannelsConfig{}
	}
	if c.Channels.Telegram == nil {
		c.Channels.Telegram = &TelegramChannelConfig{
			AllowedIDs: []int64{},
		}
	}
	if c.Channels.Telegram.AllowedIDs == nil {
		c.Channels.Telegram.AllowedIDs = []int64{}
	}
	if c.Channels.Web == nil {
		c.Channels.Web = &WebChannelConfig{}
	}
	if c.Channels.Web.Addr == "" {
		c.Channels.Web.Addr = defaultWebAddr
	}

	// Merge default cron seeds by ID (new defaults auto-appear for existing users).
	for _, seed := range DefaultConfig().Cron {
		found := false
		for _, j := range c.Cron {
			if j.ID == seed.ID {
				found = true
				break
			}
		}
		if !found {
			c.Cron = append(c.Cron, seed)
		}
	}

	def := defaultLoggingConfig()
	if c.Logging == (LoggingConfig{}) {
		c.Logging = def
		return
	}

	hasAny := c.Logging.Level != "" || c.Logging.File != "" || c.Logging.Stdout
	if c.Logging.Enabled == nil && hasAny {
		enabled := true
		c.Logging.Enabled = &enabled
	}
	if c.Logging.Level == "" {
		c.Logging.Level = def.Level
	}
	if c.Logging.File == "" {
		c.Logging.File = def.File
	}
	if !c.Logging.Stdout && c.Logging.File == "" {
		c.Logging.Stdout = def.Stdout
	}
	if c.Logging.Enabled == nil {
		c.Logging.Enabled = def.Enabled
	}
}
