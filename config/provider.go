package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/logger"
)

const (
	sessionsDirName      = "sessions"
	skillsDirName        = "skills"
	builtinSkillsDirName = "skills-builtin"
)

// SessionsDir returns the full path to the sessions directory.
func (c *Config) SessionsDir() (string, error) {
	ws, err := c.WorkspacePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(ws, sessionsDirName), nil
}

// SkillsDir returns the full path to the user-installed skills directory.
func (c *Config) SkillsDir() (string, error) {
	ws, err := c.WorkspacePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(ws, skillsDirName), nil
}

// BuiltinSkillsDir returns the full path to the built-in skills directory.
// Built-in skills are synced from embedded templates on update.
func (c *Config) BuiltinSkillsDir() (string, error) {
	ws, err := c.WorkspacePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(ws, builtinSkillsDirName), nil
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

// GetDiscordToken returns the Discord bot token (env overrides config).
func (c *Config) GetDiscordToken() string {
	if v := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN")); v != "" {
		return v
	}
	if c == nil || c.Channels == nil || c.Channels.Discord == nil {
		return ""
	}
	return c.Channels.Discord.Token
}

// GetDiscordAllowedGuildIDs returns the Discord allowed guild IDs.
func (c *Config) GetDiscordAllowedGuildIDs() []string {
	if c == nil || c.Channels == nil || c.Channels.Discord == nil {
		return nil
	}
	return c.Channels.Discord.AllowedGuildIDs
}

// GetDiscordAllowedUserIDs returns the Discord allowed user IDs.
func (c *Config) GetDiscordAllowedUserIDs() []string {
	if c == nil || c.Channels == nil || c.Channels.Discord == nil {
		return nil
	}
	return c.Channels.Discord.AllowedUserIDs
}

// GetWeComBotID returns the WeCom AI Bot ID (env overrides config).
func (c *Config) GetWeComBotID() string {
	if v := strings.TrimSpace(os.Getenv("WECOM_BOT_ID")); v != "" {
		return v
	}
	if c == nil || c.Channels == nil || c.Channels.WeCom == nil {
		return ""
	}
	return c.Channels.WeCom.BotID
}

// GetWeComSecret returns the WeCom AI Bot secret (env overrides config).
func (c *Config) GetWeComSecret() string {
	if v := strings.TrimSpace(os.Getenv("WECOM_SECRET")); v != "" {
		return v
	}
	if c == nil || c.Channels == nil || c.Channels.WeCom == nil {
		return ""
	}
	return c.Channels.WeCom.Secret
}

// GetWeComAllowedUserIDs returns the WeCom allowed user IDs.
func (c *Config) GetWeComAllowedUserIDs() []string {
	if c == nil || c.Channels == nil || c.Channels.WeCom == nil {
		return nil
	}
	return c.Channels.WeCom.AllowedUserIDs
}

// GetOAuthToken returns the OAuth token config for the given provider name.
func (c *Config) GetOAuthToken(providerName string) *OAuthTokenConfig {
	if c == nil {
		return nil
	}
	switch providerName {
	case "openai", "openai-oauth":
		return c.Providers.OpenAIOAuth
	case "anthropic", "anthropic-oauth":
		return c.Providers.AnthropicOAuth
	}
	return nil
}

// SetOAuthToken stores an OAuth token for the given provider name.
func (c *Config) SetOAuthToken(providerName string, token *OAuthTokenConfig) {
	switch providerName {
	case "openai", "openai-oauth":
		c.Providers.OpenAIOAuth = token
	case "anthropic", "anthropic-oauth":
		c.Providers.AnthropicOAuth = token
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
	if pc := c.Providers.GetProviderConfig(providerName); pc != nil {
		return pc
	}
	// Provider not found or field is nil — allocate and set it.
	// OAuth-only providers (e.g. "anthropic-oauth") have no ProviderConfig.
	pc := &ProviderConfig{}
	switch providerName {
	case "openai", "openai-oauth":
		c.Providers.OpenAI = pc
	case "openrouter":
		c.Providers.OpenRouter = pc
	case "anthropic":
		c.Providers.Anthropic = pc
	case "deepseek":
		c.Providers.DeepSeek = pc
	case "moonshot-cn":
		c.Providers.MoonshotCN = pc
	case "moonshot-global":
		c.Providers.MoonshotGlobal = pc
	case "zhipu-cn":
		c.Providers.ZhipuCN = pc
	case "zhipu-global":
		c.Providers.ZhipuGlobal = pc
	case "minimax-cn":
		c.Providers.MinimaxCN = pc
	case "minimax-global":
		c.Providers.MinimaxGlobal = pc
	case "gemini":
		c.Providers.Gemini = pc
	case "xai":
		c.Providers.XAI = pc
	default:
		return nil
	}
	return pc
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

// GetSearchKey returns the API key for a specific search provider.
func (c *Config) GetSearchKey(provider string) string {
	if c == nil || c.Tools.Web.Search.Keys == nil {
		return ""
	}
	return strings.TrimSpace(c.Tools.Web.Search.Keys[provider])
}

// GetJinaKey returns the Jina Reader API key.
func (c *Config) GetJinaKey() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Tools.Web.Fetch.JinaKey)
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
	case "openai-oauth":
		return c.Providers.OpenAI, "", "", nil
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
	case "gemini":
		return c.Providers.Gemini, "GEMINI_API_KEY", "GEMINI_API_BASE", nil
	case "xai":
		return c.Providers.XAI, "XAI_API_KEY", "XAI_API_BASE", nil
	default:
		return nil, "", "", errors.New("unknown provider: " + c.GetProvider())
	}
}
