package provider

import (
	"fmt"
	"os"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/internal/runtimecfg"
)

// FactoryConfig stores provider-level credentials and endpoint settings.
type FactoryConfig struct {
	APIKey  string
	APIBase string
}

// Factory creates provider instances for the requested provider/model.
type Factory struct {
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

	defaultProv := strings.TrimSpace(cfg.Agents.Defaults.Provider)
	if defaultProv == "" {
		return nil, fmt.Errorf("default provider is required")
	}

	defaultModel := strings.TrimSpace(cfg.Agents.Defaults.ModelType)
	if defaultModel == "" {
		return nil, fmt.Errorf("default model type is required")
	}

	if err := ValidateProviderModelType(defaultProv, defaultModel); err != nil {
		return nil, err
	}

	maxTokens := cfg.Agents.Defaults.MaxTokens
	if maxTokens == 0 {
		maxTokens = runtimecfg.AgentDefaultMaxTokens
	}

	temperature := cfg.Agents.Defaults.Temperature
	if temperature == 0 {
		temperature = runtimecfg.AgentDefaultTemperature
	}

	f := &Factory{
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

	provCfg, ok := f.configs[providerName]
	if !ok || strings.TrimSpace(provCfg.APIKey) == "" {
		return nil, fmt.Errorf("%s API key not configured", providerName)
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

	return reg.Constructor(provCfg.APIKey, provCfg.APIBase, modelType, modelName, f.maxTokens, f.temperature), nil
}

func providerAPIKey(cfg *config.Config, providerName string) string {
	reg, ok := providerRegistry[providerName]
	if !ok {
		return ""
	}
	if reg.EnvKey != "" {
		if v := strings.TrimSpace(os.Getenv(reg.EnvKey)); v != "" {
			return v
		}
	}
	if providerCfg := providerConfigFor(cfg, providerName); providerCfg != nil {
		return strings.TrimSpace(providerCfg.APIKey)
	}
	return ""
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
	case "openrouter":
		return cfg.Providers.OpenRouter
	case "anthropic":
		return cfg.Providers.Anthropic
	}
	return nil
}
