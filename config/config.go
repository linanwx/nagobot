// Package config handles configuration loading and saving.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config is the root configuration structure.
type Config struct {
	Agents    AgentsConfig    `json:"agents"`
	Providers ProvidersConfig `json:"providers"`
	Tools     ToolsConfig     `json:"tools,omitempty"`
	Channels  *ChannelsConfig `json:"channels,omitempty"`
	Logging   LoggingConfig   `json:"logging,omitempty"`
}

// AgentsConfig contains agent-related configuration.
type AgentsConfig struct {
	Defaults AgentDefaults `json:"defaults"`
}

// AgentDefaults contains default settings for agents.
type AgentDefaults struct {
	Provider          string  `json:"provider"`                    // openrouter, anthropic
	ModelType         string  `json:"modelType"`                   // moonshotai/kimi-k2.5, claude-sonnet-4-5
	ModelName         string  `json:"modelName,omitempty"`         // optional, defaults to modelType
	Workspace         string  `json:"workspace,omitempty"`         // defaults to ~/.nagobot/workspace
	MaxTokens         int     `json:"maxTokens,omitempty"`         // defaults to 8192
	Temperature       float64 `json:"temperature,omitempty"`       // defaults to 0.95
	MaxToolIterations int     `json:"maxToolIterations,omitempty"` // defaults to 20
}

// ProvidersConfig contains provider API configurations.
type ProvidersConfig struct {
	OpenRouter *ProviderConfig `json:"openrouter,omitempty"`
	Anthropic  *ProviderConfig `json:"anthropic,omitempty"`
}

// ProviderConfig contains API credentials for a provider.
type ProviderConfig struct {
	APIKey  string `json:"apiKey"`
	APIBase string `json:"apiBase,omitempty"` // optional custom base URL
}

// ToolsConfig contains tool-related configuration.
type ToolsConfig struct {
	Web  WebToolsConfig  `json:"web,omitempty"`
	Exec ExecToolsConfig `json:"exec,omitempty"`
}

// LoggingConfig contains logging configuration.
type LoggingConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Level   string `json:"level,omitempty"`  // debug, info, warn, error
	Stdout  bool   `json:"stdout,omitempty"` // log to stdout
	File    string `json:"file,omitempty"`   // log file path
}

// WebToolsConfig contains web tool configuration.
type WebToolsConfig struct {
	Search SearchConfig `json:"search,omitempty"`
}

// SearchConfig contains web search configuration.
type SearchConfig struct {
	APIKey     string `json:"apiKey,omitempty"` // Brave API key
	MaxResults int    `json:"maxResults,omitempty"`
}

// ExecToolsConfig contains exec tool configuration.
type ExecToolsConfig struct {
	Timeout             int  `json:"timeout,omitempty"`             // seconds
	RestrictToWorkspace bool `json:"restrictToWorkspace,omitempty"` // restrict to workspace
}

// ChannelsConfig contains channel configurations.
type ChannelsConfig struct {
	Telegram *TelegramChannelConfig `json:"telegram,omitempty"`
}

// TelegramChannelConfig contains Telegram bot configuration.
type TelegramChannelConfig struct {
	Token      string  `json:"token"`                // Bot token from BotFather
	AllowedIDs []int64 `json:"allowedIds,omitempty"` // Allowed user/chat IDs
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	logDefaults := defaultLoggingConfig()
	return &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Provider:          "openrouter",
				ModelType:         "moonshotai/kimi-k2.5",
				MaxTokens:         8192,
				Temperature:       0.95,
				MaxToolIterations: 20,
			},
		},
		Logging: logDefaults,
	}
}

func defaultLoggingConfig() LoggingConfig {
	dir, err := ConfigDir()
	if err != nil {
		dir = ""
	}
	logFile := filepath.Join(dir, "logs", "nagobot.log")
	enabled := true
	return LoggingConfig{
		Enabled: &enabled,
		Level:   "info",
		Stdout:  true,
		File:    logFile,
	}
}

// ConfigDir returns the nagobot config directory (~/.nagobot).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nagobot"), nil
}

// ConfigPath returns the path to config.json.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// WorkspacePath returns the workspace path, expanding ~ if needed.
func (c *Config) WorkspacePath() (string, error) {
	ws := c.Agents.Defaults.Workspace
	if ws == "" {
		dir, err := ConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "workspace"), nil
	}

	// Expand ~ to home directory
	if len(ws) > 0 && ws[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		ws = filepath.Join(home, ws[1:])
	}
	return ws, nil
}

// GetModelName returns the effective model name (modelName or modelType).
func (c *Config) GetModelName() string {
	if c.Agents.Defaults.ModelName != "" {
		return c.Agents.Defaults.ModelName
	}
	return c.Agents.Defaults.ModelType
}

// GetAPIKey returns the API key for the configured provider.
func (c *Config) GetAPIKey() (string, error) {
	switch c.Agents.Defaults.Provider {
	case "openrouter":
		if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
			return v, nil
		}
		if c.Providers.OpenRouter == nil || c.Providers.OpenRouter.APIKey == "" {
			return "", errors.New("openrouter API key not configured")
		}
		return c.Providers.OpenRouter.APIKey, nil
	case "anthropic":
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
			return v, nil
		}
		if c.Providers.Anthropic == nil || c.Providers.Anthropic.APIKey == "" {
			return "", errors.New("anthropic API key not configured")
		}
		return c.Providers.Anthropic.APIKey, nil
	default:
		return "", errors.New("unknown provider: " + c.Agents.Defaults.Provider)
	}
}

// GetAPIBase returns the API base URL for the configured provider (env overrides config).
func (c *Config) GetAPIBase() string {
	switch c.Agents.Defaults.Provider {
	case "openrouter":
		if v := os.Getenv("OPENROUTER_API_BASE"); v != "" {
			return v
		}
		if c.Providers.OpenRouter != nil && c.Providers.OpenRouter.APIBase != "" {
			return c.Providers.OpenRouter.APIBase
		}
	case "anthropic":
		if v := os.Getenv("ANTHROPIC_API_BASE"); v != "" {
			return v
		}
		if c.Providers.Anthropic != nil && c.Providers.Anthropic.APIBase != "" {
			return c.Providers.Anthropic.APIBase
		}
	}
	return ""
}

// Load loads the configuration from disk.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("config not found, run 'nagobot onboard' first")
		}
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	cfg.applyDefaults()

	return &cfg, nil
}

func (c *Config) applyDefaults() {
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

// Save saves the configuration to disk.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// EnsureWorkspace creates the workspace directory if it doesn't exist.
func (c *Config) EnsureWorkspace() error {
	ws, err := c.WorkspacePath()
	if err != nil {
		return err
	}
	return os.MkdirAll(ws, 0755)
}
