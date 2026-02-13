package provider

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/config"
)

const (
	sdkMaxRetries              = 2
	anthropicFallbackMaxTokens = 1024
	oauthExpiryGraceSec        = 30 // refresh token 30s before actual expiry
)

// FactoryConfig stores provider-level credentials and endpoint settings.
type FactoryConfig struct {
	APIKey  string
	APIBase string
}

// Factory creates provider instances for the requested provider/model.
type Factory struct {
	cfg              *config.Config // live config for OAuth token re-resolution
	configs          map[string]FactoryConfig
	defaultProv      string
	defaultModel     string
	defaultModelName string
	maxTokens        int
	temperature      float64
}

// NewFactory builds a provider factory from config.
func NewFactory(cfg *config.Config) (*Factory, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	defaultProv := strings.TrimSpace(cfg.GetProvider())
	if defaultProv == "" {
		return nil, fmt.Errorf("default provider is required")
	}

	defaultModel := strings.TrimSpace(cfg.GetModelType())
	if defaultModel == "" {
		return nil, fmt.Errorf("default model type is required")
	}

	if err := ValidateProviderModelType(defaultProv, defaultModel); err != nil {
		return nil, err
	}

	maxTokens := cfg.GetMaxTokens()
	temperature := cfg.GetTemperature()

	f := &Factory{
		cfg:              cfg,
		configs:          make(map[string]FactoryConfig),
		defaultProv:      defaultProv,
		defaultModel:     defaultModel,
		defaultModelName: cfg.GetModelName(),
		maxTokens:        maxTokens,
		temperature:      temperature,
	}

	for _, providerName := range SupportedProviders() {
		providerCfg := FactoryConfig{
			APIKey:  providerAPIKey(cfg, providerName),
			APIBase: providerAPIBase(cfg, providerName),
		}
		if providerCfg.APIKey != "" || providerName == defaultProv {
			f.configs[providerName] = providerCfg
		}
	}

	if conf, ok := f.configs[defaultProv]; !ok || strings.TrimSpace(conf.APIKey) == "" {
		return nil, fmt.Errorf("%s API key not configured", defaultProv)
	}

	return f, nil
}

// Create builds a provider instance for provider/model. Empty values fall back to defaults.
func (f *Factory) Create(providerName, modelType string) (Provider, error) {
	if f == nil {
		return nil, fmt.Errorf("provider factory is nil")
	}

	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		providerName = f.defaultProv
	}

	modelType = strings.TrimSpace(modelType)
	if modelType == "" {
		if providerName == f.defaultProv {
			modelType = f.defaultModel
		} else {
			models := SupportedModelsForProvider(providerName)
			if len(models) == 0 {
				return nil, fmt.Errorf("unknown provider: %s", providerName)
			}
			modelType = models[0]
		}
	}

	if err := ValidateProviderModelType(providerName, modelType); err != nil {
		return nil, err
	}

	// Re-resolve API key from config to pick up OAuth token refreshes.
	apiKey := providerAPIKey(f.cfg, providerName)
	provCfg, hasCfg := f.configs[providerName]
	if apiKey == "" {
		if !hasCfg || strings.TrimSpace(provCfg.APIKey) == "" {
			return nil, fmt.Errorf("%s API key not configured", providerName)
		}
		apiKey = provCfg.APIKey
	}
	reg, ok := providerRegistry[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
	if reg.Constructor == nil {
		return nil, fmt.Errorf("provider constructor not configured: %s", providerName)
	}

	modelName := modelType
	if providerName == f.defaultProv && modelType == f.defaultModel && strings.TrimSpace(f.defaultModelName) != "" {
		modelName = f.defaultModelName
	}

	apiBase := provCfg.APIBase
	p := reg.Constructor(apiKey, apiBase, modelType, modelName, f.maxTokens, f.temperature)

	// Set account ID from OAuth token if available (e.g. OpenAI ChatGPT-Account-ID).
	if setter, ok := p.(AccountIDSetter); ok {
		if token := f.cfg.GetOAuthToken(providerName); token != nil && token.AccountID != "" {
			setter.SetAccountID(token.AccountID)
		}
	}

	return p, nil
}

func providerAPIKey(cfg *config.Config, providerName string) string {
	reg, ok := providerRegistry[providerName]
	if !ok {
		return ""
	}

	// 1. Environment variable override.
	if reg.EnvKey != "" {
		if v := strings.TrimSpace(os.Getenv(reg.EnvKey)); v != "" {
			return v
		}
	}

	// 2. OAuth token (auto-refresh if expired).
	if token := cfg.GetOAuthToken(providerName); token != nil && token.AccessToken != "" {
		if token.ExpiresAt > 0 && time.Now().Unix()+oauthExpiryGraceSec > token.ExpiresAt {
			// Token expired: try refresh if possible (serialized to avoid races).
			if token.RefreshToken != "" {
				oauthRefreshMu.Lock()
				// Re-check after acquiring lock: another goroutine may have refreshed.
				if t := cfg.GetOAuthToken(providerName); t != nil && t.AccessToken != "" &&
					(t.ExpiresAt == 0 || time.Now().Unix()+oauthExpiryGraceSec <= t.ExpiresAt) {
					oauthRefreshMu.Unlock()
					return t.AccessToken
				}
				refreshed := oauthRefresher(cfg, providerName)
				oauthRefreshMu.Unlock()
				if refreshed != "" {
					return refreshed
				}
			}
			// Expired and refresh failed or unavailable — fall through to static key.
		} else {
			return token.AccessToken
		}
	}

	// 3. Static API key from config.
	if providerCfg := providerConfigFor(cfg, providerName); providerCfg != nil {
		return strings.TrimSpace(providerCfg.APIKey)
	}
	return ""
}

// oauthRefreshMu protects concurrent access to the refresh flow.
var oauthRefreshMu sync.Mutex

// oauthRefresher refreshes an expired OAuth token. Set by cmd package via SetOAuthRefresher.
var oauthRefresher = func(cfg *config.Config, providerName string) string {
	return "" // no-op default
}

// SetOAuthRefresher sets the function used to refresh expired OAuth tokens.
// Must be called during init(), before any concurrent access.
func SetOAuthRefresher(fn func(*config.Config, string) string) {
	oauthRefresher = fn
}

func providerAPIBase(cfg *config.Config, providerName string) string {
	reg, ok := providerRegistry[providerName]
	if !ok {
		return ""
	}
	if reg.EnvBase != "" {
		if v := strings.TrimSpace(os.Getenv(reg.EnvBase)); v != "" {
			return v
		}
	}
	if providerCfg := providerConfigFor(cfg, providerName); providerCfg != nil {
		return strings.TrimSpace(providerCfg.APIBase)
	}
	return ""
}

func providerConfigFor(cfg *config.Config, providerName string) *config.ProviderConfig {
	if cfg == nil {
		return nil
	}

	switch providerName {
	case "openai":
		return cfg.Providers.OpenAI
	case "openrouter":
		return cfg.Providers.OpenRouter
	case "anthropic":
		return cfg.Providers.Anthropic
	case "deepseek":
		return cfg.Providers.DeepSeek
	case "moonshot-cn":
		return cfg.Providers.MoonshotCN
	case "moonshot-global":
		return cfg.Providers.MoonshotGlobal
	case "zhipu-cn":
		return cfg.Providers.ZhipuCN
	case "zhipu-global":
		return cfg.Providers.ZhipuGlobal
	case "minimax-cn":
		return cfg.Providers.MinimaxCN
	case "minimax-global":
		return cfg.Providers.MinimaxGlobal
	}
	return nil
}
