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
	siliconflowCNAPIBase     = "https://api.siliconflow.cn/v1"
	siliconflowGlobalAPIBase = "https://api.siliconflow.com/v1"
)

func init() {
	RegisterProvider("siliconflow-cn", ProviderRegistration{
		Models: []string{"Pro/zai-org/GLM-5.1"},
		ContextWindows: map[string]int{
			"Pro/zai-org/GLM-5.1": 202752,
		},
		EnvKey:  "SILICONFLOW_API_KEY",
		EnvBase: "SILICONFLOW_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newSiliconflowProvider("siliconflow-cn", apiKey, apiBase, siliconflowCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("siliconflow-global", ProviderRegistration{
		Models: []string{"zai-org/GLM-5.1"},
		ContextWindows: map[string]int{
			"zai-org/GLM-5.1": 202752,
		},
		EnvKey:  "SILICONFLOW_GLOBAL_API_KEY",
		EnvBase: "SILICONFLOW_GLOBAL_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newSiliconflowProvider("siliconflow-global", apiKey, apiBase, siliconflowGlobalAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// SiliconflowProvider implements the Provider interface for SiliconFlow's GLM endpoints.
type SiliconflowProvider struct {
	providerName string
	apiKey       string
	apiBase      string
	modelName    string
	modelType    string
	maxTokens    int
	temperature  float64
	client       openai.Client
}

func siliconflowThinkingEnabled(modelType string) bool {
	m := strings.TrimSpace(modelType)
	return m == "Pro/zai-org/GLM-5.1" || m == "zai-org/GLM-5.1"
}

func siliconflowRequestTemperature(modelType string, configured float64) (float64, bool) {
	if siliconflowThinkingEnabled(modelType) {
		return 1, configured != 1
	}
	return configured, false
}

func newSiliconflowProvider(providerName, apiKey, apiBase, defaultBase, modelType, modelName string, maxTokens int, temperature float64) *SiliconflowProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, defaultBase, "/chat/completions")
	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &SiliconflowProvider{
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

// Chat sends a chat completion request to SiliconFlow.
func (p *SiliconflowProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, false, false, false)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	thinkingEnabled := siliconflowThinkingEnabled(p.modelType)
	logger.Info(
		"siliconflow request",
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

	requestTemp, forced := siliconflowRequestTemperature(p.modelType, p.temperature)
	if requestTemp != 0 {
		chatReq.Temperature = openai.Float(requestTemp)
	}
	if forced {
		logger.Info(
			"siliconflow temperature adjusted for thinking constraints",
			"provider", p.providerName,
			"modelType", p.modelType,
			"configuredTemperature", p.temperature,
			"requestTemperature", requestTemp,
		)
	}

	resp := &Response{ProviderLabel: p.providerName, ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()

		chatResp, streamReasoning, err := openAIStreamChat(ctx, p.client, chatReq, adapter)
		if err != nil {
			logger.Error("siliconflow request send error", "provider", p.providerName, "err", err)
			adapter.SetError(fmt.Errorf("request failed: %w", err))
			return
		}

		if len(chatResp.Choices) == 0 {
			logger.Error("siliconflow no choices", "provider", p.providerName)
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
		finalContent := choice.Message.Content
		finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "siliconflow", toolCalls)

		logger.Info(
			"siliconflow response",
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
