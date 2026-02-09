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
	moonshotCNAPIBase     = "https://api.moonshot.cn/v1"
	moonshotGlobalAPIBase = "https://api.moonshot.ai/v1"
)

func init() {
	RegisterProvider("moonshot-cn", ProviderRegistration{
		Models:  []string{"kimi-k2.5"},
		EnvKey:  "MOONSHOT_API_KEY",
		EnvBase: "MOONSHOT_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return NewMoonshotProvider("moonshot-cn", apiKey, apiBase, moonshotCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("moonshot-global", ProviderRegistration{
		Models:  []string{"kimi-k2.5"},
		EnvKey:  "MOONSHOT_GLOBAL_API_KEY",
		EnvBase: "MOONSHOT_GLOBAL_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return NewMoonshotProvider("moonshot-global", apiKey, apiBase, moonshotGlobalAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// MoonshotProvider implements the Provider interface for Moonshot native API.
type MoonshotProvider struct {
	providerName string
	apiKey       string
	apiBase      string
	modelName    string
	modelType    string
	maxTokens    int
	temperature  float64
	client       openai.Client
}

func moonshotRequestTemperature(modelType string, configured float64) (float64, bool) {
	if strings.TrimSpace(modelType) == "kimi-k2.5" {
		return 1, true
	}
	return configured, false
}

// NewMoonshotProvider creates a new Moonshot provider.
func NewMoonshotProvider(providerName, apiKey, apiBase, defaultBase, modelType, modelName string, maxTokens int, temperature float64) *MoonshotProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, defaultBase, "/chat/completions")
	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &MoonshotProvider{
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

// Chat sends a chat completion request to Moonshot.
func (p *MoonshotProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := openRouterInputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	logger.Info(
		"moonshot request",
		"provider", p.providerName,
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
	requestTemp, forced := moonshotRequestTemperature(p.modelType, p.temperature)
	if requestTemp != 0 {
		chatReq.Temperature = openai.Float(requestTemp)
	}
	if forced && p.temperature != requestTemp {
		logger.Warn(
			"moonshot temperature adjusted for model constraints",
			"provider", p.providerName,
			"modelType", p.modelType,
			"configuredTemperature", p.temperature,
			"requestTemperature", requestTemp,
		)
	}

	chatResp, err := p.client.Chat.Completions.New(ctx, chatReq)
	if err != nil {
		logger.Error("moonshot request send error", "provider", p.providerName, "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		logger.Error("moonshot no choices", "provider", p.providerName)
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
		logger.Warn("moonshot response content empty, using reasoning text fallback")
		finalContent = reasoningText
	}

	logger.Info(
		"moonshot response",
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
	logger.Debug(
		"moonshot raw output",
		"provider", p.providerName,
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
