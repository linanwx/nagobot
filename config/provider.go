package config

import (
	"errors"
	"os"
	"strings"
)

// GetProvider returns the configured default thread provider.
func (c *Config) GetProvider() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Thread.Provider)
}

// GetModelType returns the configured default thread model type.
func (c *Config) GetModelType() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Thread.ModelType)
}

// GetModelName returns the effective model name (modelName or modelType).
func (c *Config) GetModelName() string {
	if c == nil {
		return ""
	}
	if v := strings.TrimSpace(c.Thread.ModelName); v != "" {
		return v
	}
	return c.GetModelType()
}

// GetMaxTokens returns the configured default max tokens for thread provider requests.
func (c *Config) GetMaxTokens() int {
	if c == nil {
		return 0
	}
	return c.Thread.MaxTokens
}

// GetTemperature returns the configured default sampling temperature.
func (c *Config) GetTemperature() float64 {
	if c == nil {
		return 0
	}
	return c.Thread.Temperature
}

// GetContextWindowTokens returns the configured context window size.
func (c *Config) GetContextWindowTokens() int {
	if c == nil {
		return 0
	}
	return c.Thread.ContextWindowTokens
}

// GetContextWarnRatio returns the configured context pressure warning ratio.
func (c *Config) GetContextWarnRatio() float64 {
	if c == nil {
		return 0
	}
	return c.Thread.ContextWarnRatio
}

// GetAdminUserID returns the cross-channel admin user ID.
func (c *Config) GetAdminUserID() string {
	if c == nil || c.Channels == nil {
		return ""
	}
	return c.Channels.AdminUserID
}

// GetAPIKey returns the API key for the configured provider.
func (c *Config) GetAPIKey() (string, error) {
	providerCfg, envKey, _, err := c.providerConfigEnv()
	if err != nil {
		return "", err
	}
	if envKey != "" {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			return v, nil
		}
	}
	if providerCfg == nil || strings.TrimSpace(providerCfg.APIKey) == "" {
		return "", errors.New(c.GetProvider() + " API key not configured")
	}
	return providerCfg.APIKey, nil
}

// GetAPIBase returns the API base URL for the configured provider (env overrides config).
func (c *Config) GetAPIBase() string {
	providerCfg, _, envBase, err := c.providerConfigEnv()
	if err != nil {
		return ""
	}
	if envBase != "" {
		if v := strings.TrimSpace(os.Getenv(envBase)); v != "" {
			return v
		}
	}
	if providerCfg != nil {
		return strings.TrimSpace(providerCfg.APIBase)
	}
	return ""
}

func (c *Config) providerConfigEnv() (*ProviderConfig, string, string, error) {
	switch c.GetProvider() {
	case "openrouter":
		return c.Providers.OpenRouter, "OPENROUTER_API_KEY", "OPENROUTER_API_BASE", nil
	case "anthropic":
		return c.Providers.Anthropic, "ANTHROPIC_API_KEY", "ANTHROPIC_API_BASE", nil
	case "deepseek":
		return c.Providers.DeepSeek, "DEEPSEEK_API_KEY", "DEEPSEEK_API_BASE", nil
	case "moonshot-cn":
		return c.Providers.MoonshotCN, "MOONSHOT_API_KEY", "MOONSHOT_API_BASE", nil
	case "moonshot-global":
		return c.Providers.MoonshotGlobal, "MOONSHOT_GLOBAL_API_KEY", "MOONSHOT_GLOBAL_API_BASE", nil
	default:
		return nil, "", "", errors.New("unknown provider: " + c.GetProvider())
	}
}
