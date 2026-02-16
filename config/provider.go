package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/logger"
)

const (
	sessionsDirName = "sessions"
	skillsDirName   = "skills"
)

// SessionsDir returns the full path to the sessions directory.
func (c *Config) SessionsDir() (string, error) {
	ws, err := c.WorkspacePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(ws, sessionsDirName), nil
}

// SkillsDir returns the full path to the skills directory.
func (c *Config) SkillsDir() (string, error) {
	ws, err := c.WorkspacePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(ws, skillsDirName), nil
}

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

// GetWebAddr returns the configured web channel listen address.
func (c *Config) GetWebAddr() string {
	if c == nil || c.Channels == nil || c.Channels.Web == nil {
		return ""
	}
	return strings.TrimSpace(c.Channels.Web.Addr)
}

// GetTelegramToken returns the Telegram bot token (env overrides config).
func (c *Config) GetTelegramToken() string {
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); v != "" {
		return v
	}
	if c == nil || c.Channels == nil || c.Channels.Telegram == nil {
		return ""
	}
	return c.Channels.Telegram.Token
}

// GetTelegramAllowedIDs returns the Telegram allowed user/chat IDs.
func (c *Config) GetTelegramAllowedIDs() []int64 {
	if c == nil || c.Channels == nil || c.Channels.Telegram == nil {
		return nil
	}
	return c.Channels.Telegram.AllowedIDs
}

// GetFeishuAppID returns the Feishu app ID (env overrides config).
func (c *Config) GetFeishuAppID() string {
	if v := strings.TrimSpace(os.Getenv("FEISHU_APP_ID")); v != "" {
		return v
	}
	if c == nil || c.Channels == nil || c.Channels.Feishu == nil {
		return ""
	}
	return c.Channels.Feishu.AppID
}

// GetFeishuAppSecret returns the Feishu app secret (env overrides config).
func (c *Config) GetFeishuAppSecret() string {
	if v := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET")); v != "" {
		return v
	}
	if c == nil || c.Channels == nil || c.Channels.Feishu == nil {
		return ""
	}
	return c.Channels.Feishu.AppSecret
}

// GetFeishuVerificationToken returns the Feishu verification token.
func (c *Config) GetFeishuVerificationToken() string {
	if c == nil || c.Channels == nil || c.Channels.Feishu == nil {
		return ""
	}
	return c.Channels.Feishu.VerificationToken
}

// GetFeishuEncryptKey returns the Feishu encrypt key.
func (c *Config) GetFeishuEncryptKey() string {
	if c == nil || c.Channels == nil || c.Channels.Feishu == nil {
		return ""
	}
	return c.Channels.Feishu.EncryptKey
}

// GetFeishuWebhookAddr returns the Feishu webhook listen address (default 127.0.0.1:9090).
func (c *Config) GetFeishuWebhookAddr() string {
	if c == nil || c.Channels == nil || c.Channels.Feishu == nil {
		return "127.0.0.1:9090"
	}
	if v := strings.TrimSpace(c.Channels.Feishu.WebhookAddr); v != "" {
		return v
	}
	return "127.0.0.1:9090"
}

// GetFeishuAdminOpenID returns the Feishu admin open ID.
func (c *Config) GetFeishuAdminOpenID() string {
	if c == nil || c.Channels == nil || c.Channels.Feishu == nil {
		return ""
	}
	return c.Channels.Feishu.AdminOpenID
}

// GetFeishuAllowedOpenIDs returns the Feishu allowed open IDs.
func (c *Config) GetFeishuAllowedOpenIDs() []string {
	if c == nil || c.Channels == nil || c.Channels.Feishu == nil {
		return nil
	}
	return c.Channels.Feishu.AllowedOpenIDs
}

// GetOAuthToken returns the OAuth token config for the given provider name.
func (c *Config) GetOAuthToken(providerName string) *OAuthTokenConfig {
	if c == nil {
		return nil
	}
	switch providerName {
	case "openai":
		return c.Providers.OpenAIOAuth
	}
	return nil
}

// SetOAuthToken stores an OAuth token for the given provider name.
func (c *Config) SetOAuthToken(providerName string, token *OAuthTokenConfig) {
	switch providerName {
	case "openai":
		c.Providers.OpenAIOAuth = token
	}
}

// ClearOAuthToken removes the OAuth token for the given provider name.
func (c *Config) ClearOAuthToken(providerName string) {
	c.SetOAuthToken(providerName, nil)
}

// ensureProviderConfig returns a mutable *ProviderConfig for the current
// provider, creating it if nil.
// EnsureProviderConfigFor returns the ProviderConfig for the given provider name,
// creating it if it does not exist.
func (c *Config) EnsureProviderConfigFor(providerName string) *ProviderConfig {
	switch providerName {
	case "openai":
		if c.Providers.OpenAI == nil {
			c.Providers.OpenAI = &ProviderConfig{}
		}
		return c.Providers.OpenAI
	case "openrouter":
		if c.Providers.OpenRouter == nil {
			c.Providers.OpenRouter = &ProviderConfig{}
		}
		return c.Providers.OpenRouter
	case "anthropic":
		if c.Providers.Anthropic == nil {
			c.Providers.Anthropic = &ProviderConfig{}
		}
		return c.Providers.Anthropic
	case "deepseek":
		if c.Providers.DeepSeek == nil {
			c.Providers.DeepSeek = &ProviderConfig{}
		}
		return c.Providers.DeepSeek
	case "moonshot-cn":
		if c.Providers.MoonshotCN == nil {
			c.Providers.MoonshotCN = &ProviderConfig{}
		}
		return c.Providers.MoonshotCN
	case "moonshot-global":
		if c.Providers.MoonshotGlobal == nil {
			c.Providers.MoonshotGlobal = &ProviderConfig{}
		}
		return c.Providers.MoonshotGlobal
	case "zhipu-cn":
		if c.Providers.ZhipuCN == nil {
			c.Providers.ZhipuCN = &ProviderConfig{}
		}
		return c.Providers.ZhipuCN
	case "zhipu-global":
		if c.Providers.ZhipuGlobal == nil {
			c.Providers.ZhipuGlobal = &ProviderConfig{}
		}
		return c.Providers.ZhipuGlobal
	case "minimax-cn":
		if c.Providers.MinimaxCN == nil {
			c.Providers.MinimaxCN = &ProviderConfig{}
		}
		return c.Providers.MinimaxCN
	case "minimax-global":
		if c.Providers.MinimaxGlobal == nil {
			c.Providers.MinimaxGlobal = &ProviderConfig{}
		}
		return c.Providers.MinimaxGlobal
	default:
		return &ProviderConfig{}
	}
}

func (c *Config) ensureProviderConfig() *ProviderConfig {
	return c.EnsureProviderConfigFor(c.GetProvider())
}

// SetProviderAPIKey sets the API key on the current provider config.
func (c *Config) SetProviderAPIKey(key string) {
	c.ensureProviderConfig().APIKey = key
}

// SetProviderAPIBase sets the API base URL on the current provider config.
func (c *Config) SetProviderAPIBase(base string) {
	c.ensureProviderConfig().APIBase = base
}

// GetExecTimeout returns the exec tool timeout in seconds.
func (c *Config) GetExecTimeout() int {
	if c == nil {
		return 0
	}
	return c.Tools.Exec.Timeout
}

// GetExecRestrictToWorkspace returns whether exec is restricted to workspace.
func (c *Config) GetExecRestrictToWorkspace() bool {
	if c == nil {
		return false
	}
	return c.Tools.Exec.RestrictToWorkspace
}

// GetWebSearchMaxResults returns the web search max results.
func (c *Config) GetWebSearchMaxResults() int {
	if c == nil {
		return 0
	}
	return c.Tools.Web.Search.MaxResults
}

// BuildLoggerConfig returns a logger.Config ready for logger.Init().
func (c *Config) BuildLoggerConfig() logger.Config {
	enabled := true
	if c != nil && c.Logging.Enabled != nil {
		enabled = *c.Logging.Enabled
	}
	return logger.Config{
		Enabled: enabled,
		Level:   c.Logging.Level,
		Stdout:  c.Logging.Stdout,
		File:    c.Logging.File,
	}
}

// SetLoggingLevel sets the logging level.
func (c *Config) SetLoggingLevel(level string) {
	c.Logging.Level = level
}

// SetProvider overrides the provider name.
func (c *Config) SetProvider(name string) {
	c.Thread.Provider = name
}

// SetModelType overrides the model type and clears the model name.
func (c *Config) SetModelType(modelType string) {
	c.Thread.ModelType = modelType
	c.Thread.ModelName = ""
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
	case "openai":
		return c.Providers.OpenAI, "OPENAI_API_KEY", "OPENAI_API_BASE", nil
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
	case "zhipu-cn":
		return c.Providers.ZhipuCN, "ZHIPU_API_KEY", "ZHIPU_API_BASE", nil
	case "zhipu-global":
		return c.Providers.ZhipuGlobal, "ZHIPU_GLOBAL_API_KEY", "ZHIPU_GLOBAL_API_BASE", nil
	case "minimax-cn":
		return c.Providers.MinimaxCN, "MINIMAX_API_KEY", "MINIMAX_API_BASE", nil
	case "minimax-global":
		return c.Providers.MinimaxGlobal, "MINIMAX_GLOBAL_API_KEY", "MINIMAX_GLOBAL_API_BASE", nil
	default:
		return nil, "", "", errors.New("unknown provider: " + c.GetProvider())
	}
}
