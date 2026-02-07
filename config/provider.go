package config

import (
	"errors"
	"os"
	"strings"
)

// GetModelName returns the effective model name (modelName or modelType).
func (c *Config) GetModelName() string {
	if c.Agents.Defaults.ModelName != "" {
		return c.Agents.Defaults.ModelName
	}
	return c.Agents.Defaults.ModelType
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
		return "", errors.New(c.Agents.Defaults.Provider + " API key not configured")
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
	switch c.Agents.Defaults.Provider {
	case "openrouter":
		return c.Providers.OpenRouter, "OPENROUTER_API_KEY", "OPENROUTER_API_BASE", nil
	case "anthropic":
		return c.Providers.Anthropic, "ANTHROPIC_API_KEY", "ANTHROPIC_API_BASE", nil
	default:
		return nil, "", "", errors.New("unknown provider: " + c.Agents.Defaults.Provider)
	}
}
