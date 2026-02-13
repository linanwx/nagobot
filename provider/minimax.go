// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	openai "github.com/openai/openai-go/v3"
	oaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const (
	minimaxCNAPIBase     = "https://api.minimaxi.com/v1"
	minimaxGlobalAPIBase = "https://api.minimax.io/v1"
)

// minimaxModelAPINames maps whitelist keys to actual API model strings.
var minimaxModelAPINames = map[string]string{
	"minimax-m2.5": "MiniMax-M2.5",
}

func init() {
	RegisterProvider("minimax-cn", ProviderRegistration{
		Models:  []string{"minimax-m2.5"},
		EnvKey:  "MINIMAX_API_KEY",
		EnvBase: "MINIMAX_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newMinimaxProvider("minimax-cn", apiKey, apiBase, minimaxCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("minimax-global", ProviderRegistration{
		Models:  []string{"minimax-m2.5"},
		EnvKey:  "MINIMAX_GLOBAL_API_KEY",
		EnvBase: "MINIMAX_GLOBAL_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newMinimaxProvider("minimax-global", apiKey, apiBase, minimaxGlobalAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// MinimaxProvider implements the Provider interface for Minimax API.
type MinimaxProvider struct {
	providerName string
	apiKey       string
	apiBase      string
	modelName    string
	modelType    string
	maxTokens    int
	temperature  float64
	client       openai.Client
}

func minimaxThinkingEnabled(modelType string) bool {
	return strings.TrimSpace(modelType) == "minimax-m2.5"
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
		} else {
			modelName = modelType
		}
	}

	baseURL := normalizeSDKBaseURL(apiBase, defaultBase, "/chat/completions")
	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &MinimaxProvider{
		providerName: providerName,
		apiKey:       apiKey,
		apiBase:      baseURL,
		modelName:    modelName,
		modelType:    modelType,
		maxTokens:    maxTokens,
		temperature:  temperature,
		client:       client,
	}
}

// Chat sends a chat completion request to Minimax.
func (p *MinimaxProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := openRouterInputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	thinkingEnabled := minimaxThinkingEnabled(p.modelType)
	logger.Info(
		"minimax request",
		"provider", p.providerName,
		"modelType", p.modelType,
		"modelName", p.modelName,
		"thinkingEnabled", thinkingEnabled,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	chatReq := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(p.modelName),
		Messages: messages,
		Tools:    toOpenAIChatTools(req.Tools),
	}
	if p.maxTokens > 0 {
		chatReq.MaxTokens = openai.Int(int64(p.maxTokens))
	}

	requestTemp, forced := minimaxRequestTemperature(p.modelType, p.temperature)
	if requestTemp != 0 {
		chatReq.Temperature = openai.Float(requestTemp)
	}
	if forced {
		logger.Warn(
			"minimax temperature adjusted for thinking constraints",
			"provider", p.providerName,
			"modelType", p.modelType,
			"configuredTemperature", p.temperature,
			"requestTemperature", requestTemp,
		)
	}

	requestOpts := []oaioption.RequestOption{}
	if thinkingEnabled {
		// Minimax uses top-level reasoning_split param (not extra_body).
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("reasoning_split", true),
		)
	}

	chatResp, err := p.client.Chat.Completions.New(ctx, chatReq, requestOpts...)
	if err != nil {
		logger.Error("minimax request send error", "provider", p.providerName, "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		logger.Error("minimax no choices", "provider", p.providerName)
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
	reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
	rawMessage := choice.Message.RawJSON()
	reasoningText := extractMinimaxReasoning(rawMessage)
	finalContent := choice.Message.Content
	if strings.TrimSpace(finalContent) == "" && len(toolCalls) == 0 && strings.TrimSpace(reasoningText) != "" {
		logger.Warn("minimax response content empty, using reasoning text fallback", "provider", p.providerName)
		finalContent = reasoningText
	}

	logger.Info(
		"minimax response",
		"provider", p.providerName,
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", choice.FinishReason,
		"reasoningInResponse", reasoningTokens > 0 || strings.TrimSpace(reasoningText) != "",
		"hasToolCalls", len(toolCalls) > 0,
		"toolCallCount", len(toolCalls),
		"promptTokens", chatResp.Usage.PromptTokens,
		"completionTokens", chatResp.Usage.CompletionTokens,
		"reasoningTokens", reasoningTokens,
		"totalTokens", chatResp.Usage.TotalTokens,
		"outputChars", len(choice.Message.Content),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return &Response{
		Content:          finalContent,
		ReasoningContent: reasoningText,
		ToolCalls:        toolCalls,
		Usage: Usage{
			PromptTokens:     int(chatResp.Usage.PromptTokens),
			CompletionTokens: int(chatResp.Usage.CompletionTokens),
			TotalTokens:      int(chatResp.Usage.TotalTokens),
		},
	}, nil
}
