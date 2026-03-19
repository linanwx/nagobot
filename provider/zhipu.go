// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"strings"

	oaioption "github.com/openai/openai-go/v3/option"
)

const (
	zhipuCNAPIBase     = "https://open.bigmodel.cn/api/paas/v4"
	zhipuGlobalAPIBase = "https://api.z.ai/api/paas/v4"
)

func init() {
	RegisterProvider("zhipu-cn", ProviderRegistration{
		Models: []string{"glm-5"},
		ContextWindows: map[string]int{
			"glm-5": 200000,
		},
		EnvKey:  "ZHIPU_API_KEY",
		EnvBase: "ZHIPU_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newZhipuProvider("zhipu-cn", apiKey, apiBase, zhipuCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("zhipu-global", ProviderRegistration{
		Models: []string{"glm-5"},
		ContextWindows: map[string]int{
			"glm-5": 200000,
		},
		EnvKey:  "ZHIPU_GLOBAL_API_KEY",
		EnvBase: "ZHIPU_GLOBAL_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newZhipuProvider("zhipu-global", apiKey, apiBase, zhipuGlobalAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// ZhipuProvider implements the Provider interface for Zhipu GLM API.
type ZhipuProvider struct {
	sdkProviderBase
}

func zhipuThinkingEnabled(modelType string) bool {
	return strings.TrimSpace(modelType) == "glm-5"
}

func zhipuRequestTemperature(modelType string, configured float64) (float64, bool) {
	if zhipuThinkingEnabled(modelType) {
		return 1, configured != 1
	}
	return configured, false
}

func newZhipuProvider(providerName, apiKey, apiBase, defaultBase, modelType, modelName string, maxTokens int, temperature float64) *ZhipuProvider {
	return &ZhipuProvider{
		sdkProviderBase: newSDKProviderBase(providerName, apiKey, apiBase, defaultBase, modelType, modelName, maxTokens, temperature),
	}
}

// Chat sends a chat completion request to Zhipu.
func (p *ZhipuProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	requestTemp, forced := zhipuRequestTemperature(p.modelType, p.temperature)

	var requestOpts []oaioption.RequestOption
	if zhipuThinkingEnabled(p.modelType) {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("extra_body.thinking.type", "enabled"),
			oaioption.WithJSONSet("extra_body.thinking.clear_thinking", true),
		)
	}

	return p.sdkChat(ctx, req, sdkChatConfig{
		Temperature: requestTemp,
		Forced:      forced,
		RequestOpts: requestOpts,
	})
}
