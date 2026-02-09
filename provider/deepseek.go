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
	deepSeekAPIBase = "https://api.deepseek.com"
)

func init() {
	RegisterProvider("deepseek", ProviderRegistration{
		Models:  []string{"deepseek-chat", "deepseek-reasoner"},
		EnvKey:  "DEEPSEEK_API_KEY",
		EnvBase: "DEEPSEEK_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return NewDeepSeekProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// DeepSeekProvider implements the Provider interface for DeepSeek native API.
type DeepSeekProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      openai.Client
}

// NewDeepSeekProvider creates a new DeepSeek provider.
func NewDeepSeekProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *DeepSeekProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, deepSeekAPIBase, "/chat/completions")
	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &DeepSeekProvider{
		apiKey:      apiKey,
		apiBase:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      client,
	}
}

// Chat sends a chat completion request to DeepSeek.
func (p *DeepSeekProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := openRouterInputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	thinkingEnabled := strings.TrimSpace(p.modelType) == "deepseek-reasoner"
	logger.Debug(
		"deepseek request",
		"provider", "deepseek",
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
	if p.temperature != 0 && !thinkingEnabled {
		chatReq.Temperature = openai.Float(p.temperature)
	}

	requestOpts := []oaioption.RequestOption{}
	if thinkingEnabled {
		// Official DeepSeek guide: OpenAI SDK should pass thinking switch via extra_body.
		requestOpts = append(requestOpts, oaioption.WithJSONSet("extra_body.thinking.type", "enabled"))
	}

	chatResp, err := p.client.Chat.Completions.New(ctx, chatReq, requestOpts...)
	if err != nil {
		logger.Error("deepseek request send error", "provider", "deepseek", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		logger.Error("deepseek no choices", "provider", "deepseek")
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
	reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
	rawMessage := choice.Message.RawJSON()
	rawResponse := chatResp.RawJSON()
	reasoningText := extractReasoningText(rawMessage)
	finalContent := choice.Message.Content
	if strings.TrimSpace(finalContent) == "" && len(toolCalls) == 0 && strings.TrimSpace(reasoningText) != "" {
		logger.Warn("deepseek response content empty, using reasoning text fallback")
		finalContent = reasoningText
	}

	logger.Debug(
		"deepseek response",
		"provider", "deepseek",
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
	logger.Debug(
		"deepseek raw output",
		"rawMessage", rawMessage,
		"rawResponse", rawResponse,
		"reasoningText", reasoningText,
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
