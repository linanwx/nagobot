// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	openai "github.com/openai/openai-go/v3"
	oaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const (
	zhipuCNAPIBase     = "https://open.bigmodel.cn/api/paas/v4"
	zhipuGlobalAPIBase = "https://api.z.ai/api/paas/v4"
)

func init() {
	RegisterProvider("zhipu-cn", ProviderRegistration{
		Models:  []string{"glm-5"},
		EnvKey:  "ZHIPU_API_KEY",
		EnvBase: "ZHIPU_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newZhipuProvider("zhipu-cn", apiKey, apiBase, zhipuCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("zhipu-global", ProviderRegistration{
		Models:  []string{"glm-5"},
		EnvKey:  "ZHIPU_GLOBAL_API_KEY",
		EnvBase: "ZHIPU_GLOBAL_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newZhipuProvider("zhipu-global", apiKey, apiBase, zhipuGlobalAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// ZhipuProvider implements the Provider interface for Zhipu GLM API.
type ZhipuProvider struct {
	providerName string
	apiKey       string
	apiBase      string
	modelName    string
	modelType    string
	maxTokens    int
	temperature  float64
	client       openai.Client
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
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, defaultBase, "/chat/completions")
	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &ZhipuProvider{
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

// Chat sends a chat completion request to Zhipu.
func (p *ZhipuProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := openRouterInputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, false)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	thinkingEnabled := zhipuThinkingEnabled(p.modelType)
	logger.Info(
		"zhipu request",
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

	requestTemp, forced := zhipuRequestTemperature(p.modelType, p.temperature)
	if requestTemp != 0 {
		chatReq.Temperature = openai.Float(requestTemp)
	}
	if forced {
		logger.Warn(
			"zhipu temperature adjusted for thinking constraints",
			"provider", p.providerName,
			"modelType", p.modelType,
			"configuredTemperature", p.temperature,
			"requestTemperature", requestTemp,
		)
	}

	requestOpts := []oaioption.RequestOption{}
	if thinkingEnabled {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("extra_body.thinking.type", "enabled"),
			oaioption.WithJSONSet("extra_body.thinking.clear_thinking", true),
		)
	}

	chatResp, err := p.client.Chat.Completions.New(ctx, chatReq, requestOpts...)
	if err != nil {
		logger.Error("zhipu request send error", "provider", p.providerName, "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		logger.Error("zhipu no choices", "provider", p.providerName)
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
	reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
	rawMessage := choice.Message.RawJSON()
	reasoningText := extractReasoningText(rawMessage)
	finalContent := choice.Message.Content
	if strings.TrimSpace(finalContent) == "" && len(toolCalls) == 0 && strings.TrimSpace(reasoningText) != "" {
		logger.Warn("zhipu response content empty, using reasoning text fallback", "provider", p.providerName)
		finalContent = reasoningText
	}

	logger.Info(
		"zhipu response",
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
