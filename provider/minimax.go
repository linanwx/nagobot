// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"encoding/json"
	"strings"

	oaioption "github.com/openai/openai-go/v3/option"
)

const (
	minimaxCNAPIBase     = "https://api.minimaxi.com/v1"
	minimaxGlobalAPIBase = "https://api.minimax.io/v1"
)

// minimaxModelAPINames maps whitelist keys to actual API model strings.
var minimaxModelAPINames = map[string]string{
	"minimax-m2.5": "MiniMax-M2.5",
	"minimax-m2.7": "MiniMax-M2.7",
}

func init() {
	RegisterProvider("minimax-cn", ProviderRegistration{
		Models: []string{"minimax-m2.5", "minimax-m2.7"},
		ContextWindows: map[string]int{
			"minimax-m2.5": 196608,
			"minimax-m2.7": 204800,
		},
		EnvKey:  "MINIMAX_API_KEY",
		EnvBase: "MINIMAX_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newMinimaxProvider("minimax-cn", apiKey, apiBase, minimaxCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("minimax-global", ProviderRegistration{
		Models: []string{"minimax-m2.5", "minimax-m2.7"},
		ContextWindows: map[string]int{
			"minimax-m2.5": 196608,
			"minimax-m2.7": 204800,
		},
		EnvKey:  "MINIMAX_GLOBAL_API_KEY",
		EnvBase: "MINIMAX_GLOBAL_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newMinimaxProvider("minimax-global", apiKey, apiBase, minimaxGlobalAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// MinimaxProvider implements the Provider interface for Minimax API.
type MinimaxProvider struct {
	sdkProviderBase
}

func minimaxThinkingEnabled(modelType string) bool {
	mt := strings.TrimSpace(modelType)
	return mt == "minimax-m2.5" || mt == "minimax-m2.7"
}

func minimaxRequestTemperature(modelType string, configured float64) (float64, bool) {
	if minimaxThinkingEnabled(modelType) {
		return 1, configured != 1
	}
	return configured, false
}

// minimaxReasoningDetail represents one entry in the reasoning_details array.
type minimaxReasoningDetail struct {
	Text string `json:"text"`
}

// extractMinimaxReasoning parses the reasoning_details array from a raw message JSON
// and returns the concatenated text content.
func extractMinimaxReasoning(rawMessage string) string {
	if rawMessage == "" {
		return ""
	}

	var payload struct {
		ReasoningDetails []minimaxReasoningDetail `json:"reasoning_details"`
	}
	if err := json.Unmarshal([]byte(rawMessage), &payload); err != nil {
		return ""
	}

	if len(payload.ReasoningDetails) == 0 {
		return ""
	}

	var parts []string
	for _, detail := range payload.ReasoningDetails {
		if t := strings.TrimSpace(detail.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

func newMinimaxProvider(providerName, apiKey, apiBase, defaultBase, modelType, modelName string, maxTokens int, temperature float64) *MinimaxProvider {
	// Map whitelist key to actual API model string when no override is set.
	if modelName == "" || modelName == modelType {
		if apiName, ok := minimaxModelAPINames[modelType]; ok {
			modelName = apiName
		}
	}

	return &MinimaxProvider{
		sdkProviderBase: newSDKProviderBase(providerName, apiKey, apiBase, defaultBase, modelType, modelName, maxTokens, temperature),
	}
}

// Chat sends a chat completion request to Minimax.
func (p *MinimaxProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	requestTemp, forced := minimaxRequestTemperature(p.modelType, p.temperature)

	var requestOpts []oaioption.RequestOption
	if minimaxThinkingEnabled(p.modelType) {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("reasoning_split", true),
		)
	}

	return p.sdkChat(ctx, req, sdkChatConfig{
		Temperature: requestTemp,
		Forced:      forced,
		RequestOpts: requestOpts,
		ReasoningFn: extractMinimaxReasoning,
	})
}
