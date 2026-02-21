// Package config handles configuration loading and saving.
package config

import (
	"os"
	"strings"
	"sync"
	"time"

	cronpkg "github.com/linanwx/nagobot/cron"
	"gopkg.in/yaml.v3"
)

const (
	configFileName = "config.yaml"
)

var configDirOverride string

// SetConfigDir overrides the config directory for the current process.
// Empty value clears the override.
func SetConfigDir(dir string) {
	configDirOverride = strings.TrimSpace(dir)
}

// Config is the root configuration structure.
type Config struct {
	Thread    ThreadConfig    `json:"thread" yaml:"thread"`
	Providers ProvidersConfig `json:"providers" yaml:"providers"`
	Tools     ToolsConfig     `json:"tools,omitempty" yaml:"tools,omitempty"`
	Channels  *ChannelsConfig `json:"channels" yaml:"channels"`
	Logging   LoggingConfig   `json:"logging,omitempty" yaml:"logging,omitempty"`
	Cron      []cronpkg.Job   `json:"cron,omitempty" yaml:"cron,omitempty"`

	// Hot-reload support for sessionAgents.
	sessionAgentsMu       sync.Mutex        `yaml:"-" json:"-"`
	sessionAgentsCache    map[string]string  `yaml:"-" json:"-"`
	sessionAgentsFileTime time.Time          `yaml:"-" json:"-"`
}

// SessionAgent returns the agent name for the given session key.
// It lazily reloads sessionAgents from config.yaml when the file changes on disk.
func (c *Config) SessionAgent(key string) string {
	if c == nil {
		return ""
	}
	c.sessionAgentsMu.Lock()
	defer c.sessionAgentsMu.Unlock()

	path, err := ConfigPath()
	if err != nil {
		// Fallback to in-memory config.
		if c.Channels == nil {
			return ""
		}
		return c.Channels.SessionAgents[key]
	}

	info, err := os.Stat(path)
	if err != nil || info.ModTime().Equal(c.sessionAgentsFileTime) {
		return c.sessionAgentsCache[key]
	}

	// File changed on disk — reload sessionAgents only.
	c.reloadSessionAgents(path, info.ModTime())
	return c.sessionAgentsCache[key]
}

// reloadSessionAgents reads only the channels.sessionAgents section from config.yaml.
func (c *Config) reloadSessionAgents(path string, modTime time.Time) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw struct {
		Channels struct {
			SessionAgents map[string]string `yaml:"sessionAgents"`
		} `yaml:"channels"`
	}
	if yaml.Unmarshal(data, &raw) == nil {
		c.sessionAgentsCache = raw.Channels.SessionAgents
	}
	c.sessionAgentsFileTime = modTime
}

// ThreadConfig contains thread runtime defaults.
type ThreadConfig struct {
	Provider            string                  `json:"provider" yaml:"provider"` // openrouter, anthropic, deepseek, moonshot-cn, moonshot-global
	ModelType           string                  `json:"modelType" yaml:"modelType"`
	ModelName           string                  `json:"modelName,omitempty" yaml:"modelName,omitempty"`                     // optional, defaults to modelType
	Workspace           string                  `json:"workspace,omitempty" yaml:"workspace,omitempty"`                     // defaults to ~/.nagobot/workspace
	MaxTokens           int                     `json:"maxTokens,omitempty" yaml:"maxTokens,omitempty"`                     // defaults to 8192
	Temperature         float64                 `json:"temperature,omitempty" yaml:"temperature,omitempty"`                 // defaults to 0.95
	ContextWindowTokens int                     `json:"contextWindowTokens,omitempty" yaml:"contextWindowTokens,omitempty"` // defaults to 128000
	ContextWarnRatio    float64                 `json:"contextWarnRatio,omitempty" yaml:"contextWarnRatio,omitempty"`       // defaults to 0.8
	Models              map[string]*ModelConfig `json:"models,omitempty" yaml:"models,omitempty"`                           // model type → provider/model mapping
}

// ModelConfig maps a model type to a concrete provider and model.
type ModelConfig struct {
	Provider  string `json:"provider" yaml:"provider"`
	ModelType string `json:"modelType" yaml:"modelType"`
}

// ProvidersConfig contains provider API configurations.
type ProvidersConfig struct {
	OpenRouter     *ProviderConfig   `json:"openrouter,omitempty" yaml:"openrouter,omitempty"`
	Anthropic      *ProviderConfig   `json:"anthropic,omitempty" yaml:"anthropic,omitempty"`
	DeepSeek       *ProviderConfig   `json:"deepseek,omitempty" yaml:"deepseek,omitempty"`
	MoonshotCN     *ProviderConfig   `json:"moonshotCN,omitempty" yaml:"moonshotCN,omitempty"`
	MoonshotGlobal *ProviderConfig   `json:"moonshotGlobal,omitempty" yaml:"moonshotGlobal,omitempty"`
	ZhipuCN        *ProviderConfig   `json:"zhipuCN,omitempty" yaml:"zhipuCN,omitempty"`
	ZhipuGlobal    *ProviderConfig   `json:"zhipuGlobal,omitempty" yaml:"zhipuGlobal,omitempty"`
	MinimaxCN      *ProviderConfig   `json:"minimaxCN,omitempty" yaml:"minimaxCN,omitempty"`
	MinimaxGlobal  *ProviderConfig   `json:"minimaxGlobal,omitempty" yaml:"minimaxGlobal,omitempty"`
	OpenAI         *ProviderConfig   `json:"openai,omitempty" yaml:"openai,omitempty"`
	OpenAIOAuth *OAuthTokenConfig `json:"openaiOAuth,omitempty" yaml:"openaiOAuth,omitempty"`
}

// OAuthTokenConfig stores an OAuth token with optional refresh capability.
type OAuthTokenConfig struct {
	AccessToken  string `json:"accessToken" yaml:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty" yaml:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty" yaml:"expiresAt,omitempty"`   // unix timestamp, 0 = no expiry
	TokenType    string `json:"tokenType,omitempty" yaml:"tokenType,omitempty"`   // "bearer"
	AccountID    string `json:"accountId,omitempty" yaml:"accountId,omitempty"`   // e.g. ChatGPT account ID from id_token
}

// ProviderConfig contains API credentials for a provider.
type ProviderConfig struct {
	APIKey  string `json:"apiKey" yaml:"apiKey"`
	APIBase string `json:"apiBase,omitempty" yaml:"apiBase,omitempty"` // optional custom base URL
}

// ToolsConfig contains tool-related configuration.
type ToolsConfig struct {
	Web  WebToolsConfig  `json:"web,omitempty" yaml:"web,omitempty"`
	Exec ExecToolsConfig `json:"exec,omitempty" yaml:"exec,omitempty"`
}

// LoggingConfig contains logging configuration.
type LoggingConfig struct {
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Level   string `json:"level,omitempty" yaml:"level,omitempty"`   // debug, info, warn, error
	Stdout  bool   `json:"stdout,omitempty" yaml:"stdout,omitempty"` // log to stdout
	File    string `json:"file,omitempty" yaml:"file,omitempty"`     // log file path
}

// WebToolsConfig contains web tool configuration.
type WebToolsConfig struct {
	Search SearchConfig `json:"search,omitempty" yaml:"search,omitempty"`
}

// SearchConfig contains web search configuration.
type SearchConfig struct {
	APIKey     string `json:"apiKey,omitempty" yaml:"apiKey,omitempty"` // Brave API key
	MaxResults int    `json:"maxResults,omitempty" yaml:"maxResults,omitempty"`
}

// ExecToolsConfig contains exec tool configuration.
type ExecToolsConfig struct {
	Timeout             int  `json:"timeout,omitempty" yaml:"timeout,omitempty"`                         // seconds
	RestrictToWorkspace bool `json:"restrictToWorkspace,omitempty" yaml:"restrictToWorkspace,omitempty"` // restrict to workspace
}

// ChannelsConfig contains channel configurations.
type ChannelsConfig struct {
	AdminUserID string                 `json:"adminUserID" yaml:"adminUserID"`                   // Cross-channel admin user id
	SessionAgents map[string]string    `json:"sessionAgents,omitempty" yaml:"sessionAgents,omitempty"` // sessionKey or userID → agent name
	Telegram    *TelegramChannelConfig `json:"telegram" yaml:"telegram"`
	Feishu      *FeishuChannelConfig   `json:"feishu,omitempty" yaml:"feishu,omitempty"`
	Discord     *DiscordChannelConfig  `json:"discord,omitempty" yaml:"discord,omitempty"`
	Web         *WebChannelConfig      `json:"web,omitempty" yaml:"web,omitempty"`
}

// TelegramChannelConfig contains Telegram bot configuration.
type TelegramChannelConfig struct {
	Token      string  `json:"token" yaml:"token"`           // Bot token from BotFather
	AllowedIDs []int64 `json:"allowedIds" yaml:"allowedIds"` // Allowed user/chat IDs
}

// FeishuChannelConfig contains Feishu (Lark) bot configuration.
type FeishuChannelConfig struct {
	AppID             string   `json:"appId" yaml:"appId"`
	AppSecret         string   `json:"appSecret" yaml:"appSecret"`
	VerificationToken string   `json:"verificationToken,omitempty" yaml:"verificationToken,omitempty"`
	EncryptKey        string   `json:"encryptKey,omitempty" yaml:"encryptKey,omitempty"`
	WebhookAddr       string   `json:"webhookAddr,omitempty" yaml:"webhookAddr,omitempty"` // default: 127.0.0.1:9090
	AdminOpenID       string   `json:"adminOpenId,omitempty" yaml:"adminOpenId,omitempty"`
	AllowedOpenIDs    []string `json:"allowedOpenIds,omitempty" yaml:"allowedOpenIds,omitempty"` // empty = allow all
}

// DiscordChannelConfig contains Discord bot configuration.
type DiscordChannelConfig struct {
	Token           string   `json:"token" yaml:"token"`
	AllowedGuildIDs []string `json:"allowedGuildIds,omitempty" yaml:"allowedGuildIds,omitempty"`
	AllowedUserIDs  []string `json:"allowedUserIds,omitempty" yaml:"allowedUserIds,omitempty"`
}

// WebChannelConfig contains Web chat configuration.
type WebChannelConfig struct {
	Addr string `json:"addr,omitempty" yaml:"addr,omitempty"` // default: 127.0.0.1:8080
}
