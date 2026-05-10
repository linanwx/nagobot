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
	// whatAIAPIBase matches the convention in cmd/generate_image.go: bare host,
	// /v1 segment is appended at the call site.
	whatAIAPIBase = "https://api.whatai.cc"
)

func init() {
	RegisterProvider("whatai", ProviderRegistration{
		Models:       []string{"gpt-5.4-mini", "gemini-3-flash-preview"},
		VisionModels: []string{"gpt-5.4-mini", "gemini-3-flash-preview"},
		PDFModels:    []string{"gemini-3-flash-preview"},
		ContextWindows: map[string]int{
			"gpt-5.4-mini":           400000,
			"gemini-3-flash-preview": 1048576,
		},
		EnvKey:  "WHATAI_API_KEY",
		EnvBase: "WHATAI_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newWhatAIProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// WhatAIProvider implements the Provider interface for the api.whatai.cc relay,
// which exposes an OpenAI-shape /v1/chat/completions endpoint.
type WhatAIProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      openai.Client
}

// newWhatAIProvider creates a new WhatAI provider. The configured base URL may
// or may not include the /v1 segment (project convention is to omit it); we
// normalize so the openai-go SDK always hits /v1/chat/completions.
func newWhatAIProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *WhatAIProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, whatAIAPIBase, "/chat/completions")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	}

	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &WhatAIProvider{
		apiKey:      apiKey,
		apiBase:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      client,
	}
}

// Chat sends a chat completion request to whatai.
func (p *WhatAIProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, SupportsVision("whatai", p.modelType), false, SupportsPDF("whatai", p.modelType))
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	logger.Info(
		"whatai request",
		"provider", "whatai",
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
	// gpt-5.4-mini does not accept a custom temperature on the OpenAI Responses
	// API; whatai's relay forwards the same constraint, so omit it entirely.

	resp := &Response{ProviderLabel: "whatai", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()

		chatResp, streamReasoning, _, _, err := openAIStreamChat(ctx, p.client, chatReq, adapter)
		if err != nil {
			logger.Error("whatai request send error", "provider", "whatai", "err", err)
			adapter.SetError(fmt.Errorf("request failed: %w", err))
			return
		}

		if len(chatResp.Choices) == 0 {
			logger.Error("whatai no choices", "provider", "whatai")
			adapter.SetError(fmt.Errorf("no choices in response"))
			return
		}

		choice := chatResp.Choices[0]
		toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
		reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
		rawMessage := choice.Message.RawJSON()
		reasoningText := extractReasoningText(rawMessage)
		if reasoningText == "" && streamReasoning != "" {
			reasoningText = streamReasoning
		}
		finalContent := resolveContentWithReasoningFallback(choice.Message.Content, reasoningText, "whatai", toolCalls)

		logger.Info(
			"whatai response",
			"provider", "whatai",
			"modelType", p.modelType,
			"modelName", p.modelName,
			"finishReason", choice.FinishReason,
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
