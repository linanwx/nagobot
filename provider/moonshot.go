// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"strings"

	oaioption "github.com/openai/openai-go/v3/option"
)

const (
	moonshotCNAPIBase     = "https://api.moonshot.cn/v1"
	moonshotGlobalAPIBase = "https://api.moonshot.ai/v1"
)

func init() {
	RegisterProvider("moonshot-cn", ProviderRegistration{
		Models:       []string{"kimi-k2.5"},
		VisionModels: []string{"kimi-k2.5"},
		ContextWindows: map[string]int{
			"kimi-k2.5": 262144,
		},
		EnvKey:  "MOONSHOT_API_KEY",
		EnvBase: "MOONSHOT_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newMoonshotProvider("moonshot-cn", apiKey, apiBase, moonshotCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("moonshot-global", ProviderRegistration{
		Models:       []string{"kimi-k2.5"},
		VisionModels: []string{"kimi-k2.5"},
		ContextWindows: map[string]int{
			"kimi-k2.5": 262144,
		},
		EnvKey:  "MOONSHOT_GLOBAL_API_KEY",
		EnvBase: "MOONSHOT_GLOBAL_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newMoonshotProvider("moonshot-global", apiKey, apiBase, moonshotGlobalAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// MoonshotProvider implements the Provider interface for Moonshot native API.
type MoonshotProvider struct {
	sdkProviderBase
}

func moonshotRequestTemperature(modelType string, configured float64) (float64, bool) {
	if strings.TrimSpace(modelType) == "kimi-k2.5" {
		return 1, true
	}
	return configured, false
}

// newMoonshotProvider creates a new Moonshot provider.
func newMoonshotProvider(providerName, apiKey, apiBase, defaultBase, modelType, modelName string, maxTokens int, temperature float64) *MoonshotProvider {
	return &MoonshotProvider{
		sdkProviderBase: newSDKProviderBase(providerName, apiKey, apiBase, defaultBase, modelType, modelName, maxTokens, temperature),
	}
}

// Chat sends a chat completion request to Moonshot.
func (p *MoonshotProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	requestTemp, forced := moonshotRequestTemperature(p.modelType, p.temperature)

	var requestOpts []oaioption.RequestOption
	if strings.TrimSpace(p.modelType) == "kimi-k2.5" {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("extra_body.chat_template_kwargs.thinking", true),
		)
	}

	return p.sdkChat(ctx, req, sdkChatConfig{
		VisionCapable: SupportsVision(p.providerName, p.modelType),
		Temperature:   requestTemp,
		Forced:        forced,
		RequestOpts:   requestOpts,
	})
}
