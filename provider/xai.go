// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/linanwx/nagobot/logger"
	openai "github.com/openai/openai-go/v3"
	oaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const xaiAPIBase = "https://api.x.ai/v1"

func init() {
	RegisterProvider("xai", ProviderRegistration{
		Models: []string{
			"grok-4-1-fast-reasoning",
			"grok-4-1-fast-non-reasoning",
			"grok-4.20-0309-reasoning",
			"grok-4.20-0309-non-reasoning",
		},
		VisionModels: []string{
			"grok-4-1-fast-reasoning",
			"grok-4-1-fast-non-reasoning",
			"grok-4.20-0309-reasoning",
			"grok-4.20-0309-non-reasoning",
		},
		ContextWindows: map[string]int{
			"grok-4-1-fast-reasoning":      2000000,
			"grok-4-1-fast-non-reasoning":  2000000,
			"grok-4.20-0309-reasoning":     2000000,
			"grok-4.20-0309-non-reasoning": 2000000,
		},
		EnvKey:  "XAI_API_KEY",
		EnvBase: "XAI_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newXAIProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// XAIProvider implements the Provider interface for xAI's Grok API.
// xAI exposes an OpenAI-compatible /v1/chat/completions endpoint.
type XAIProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      openai.Client
}

func newXAIProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *XAIProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, xaiAPIBase, "/chat/completions")
	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &XAIProvider{
		apiKey:      apiKey,
		apiBase:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      client,
	}
}

// Chat sends a chat completion request to xAI.
func (p *XAIProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, SupportsVision("xai", p.modelType), false, false)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	logger.Info(
		"xai request",
		"modelType", p.modelType,
		"modelName", p.modelName,
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
	if p.temperature != 0 {
		chatReq.Temperature = openai.Float(p.temperature)
	}

	resp := &Response{ProviderLabel: "xai", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()

		chatResp, streamReasoning, err := openAIStreamChat(ctx, p.client, chatReq, adapter)
		if err != nil {
			logger.Error("xai request send error", "err", err)
			adapter.SetError(fmt.Errorf("request failed: %w", err))
			return
		}

		if len(chatResp.Choices) == 0 {
			logger.Error("xai no choices")
			adapter.SetError(fmt.Errorf("no choices in response"))
			return
		}

		choice := chatResp.Choices[0]
		toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
		reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
		rawMessage := choice.Message.RawJSON()
		rawResponse := chatResp.RawJSON()
		reasoningText := extractReasoningText(rawMessage)
		if reasoningText == "" && streamReasoning != "" {
			reasoningText = streamReasoning
		}
		finalContent := choice.Message.Content
		finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "xai", toolCalls)

		logger.Info(
			"xai response",
			"modelType", p.modelType,
			"modelName", p.modelName,
			"finishReason", choice.FinishReason,
			"reasoningInResponse", reasoningTokens > 0,
			"hasToolCalls", len(toolCalls) > 0,
			"toolCallCount", len(toolCalls),
			"promptTokens", chatResp.Usage.PromptTokens,
			"completionTokens", chatResp.Usage.CompletionTokens,
			"reasoningTokens", reasoningTokens,
			"cachedTokens", chatResp.Usage.PromptTokensDetails.CachedTokens,
			"totalTokens", chatResp.Usage.TotalTokens,
			"outputChars", len(choice.Message.Content),
			"latencyMs", time.Since(start).Milliseconds(),
		)
		logger.Debug(
			"xai raw output",
			"rawMessage", rawMessage,
			"rawResponse", rawResponse,
			"reasoningText", reasoningText,
		)

		resp.Content = finalContent
		resp.ReasoningContent = reasoningText
		resp.ToolCalls = toolCalls
		resp.Usage = Usage{
			PromptTokens:     int(chatResp.Usage.PromptTokens),
			CompletionTokens: int(chatResp.Usage.CompletionTokens),
			TotalTokens:      int(chatResp.Usage.TotalTokens),
			CachedTokens:     int(chatResp.Usage.PromptTokensDetails.CachedTokens),
			ReasoningTokens:  int(reasoningTokens),
		}
	}()

	return adapter.Result(), nil
}
