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

// sdkProviderBase holds the common fields for all OpenAI-SDK-based providers.
type sdkProviderBase struct {
	providerName string
	apiKey       string
	apiBase      string
	modelName    string
	modelType    string
	maxTokens    int
	temperature  float64
	client       openai.Client
}

// newSDKProviderBase constructs the common base for SDK-based providers.
// extraOpts are appended after the standard apiKey/baseURL/maxRetries options.
func newSDKProviderBase(providerName, apiKey, apiBase, defaultBase, modelType, modelName string, maxTokens int, temperature float64, extraOpts ...oaioption.RequestOption) sdkProviderBase {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, defaultBase, "/chat/completions")
	opts := []oaioption.RequestOption{
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithMaxRetries(sdkMaxRetries),
	}
	opts = append(opts, extraOpts...)
	client := openai.NewClient(opts...)

	return sdkProviderBase{
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

// sdkChatConfig controls provider-specific behavior in sdkChat.
type sdkChatConfig struct {
	// VisionCapable indicates whether this provider+model supports image input.
	VisionCapable bool
	// RequestOpts are extra options passed to the Chat.Completions.New call.
	RequestOpts []oaioption.RequestOption
	// Temperature and Forced indicate the request-time temperature override.
	Temperature float64
	Forced      bool
	// ReasoningFn extracts reasoning text from the raw message JSON.
	// Defaults to extractReasoningText if nil.
	ReasoningFn func(rawMessage string) string
	// ExtraReasoningDetails if true, also extracts reasoningDetails (e.g. openrouter).
	ExtraReasoningDetails bool
}

// sdkChat implements the common Chat() flow for OpenAI-SDK-based providers.
func (b *sdkProviderBase) sdkChat(ctx context.Context, req *Request, cfg sdkChatConfig) (*Response, error) {
	start := time.Now()
	ic := inputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, cfg.VisionCapable)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	logger.Info(
		b.providerName+" request",
		"provider", b.providerName,
		"modelType", b.modelType,
		"modelName", b.modelName,
		"toolCount", len(req.Tools),
		"inputChars", ic,
	)

	chatReq := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(b.modelName),
		Messages: messages,
		Tools:    toOpenAIChatTools(req.Tools),
	}
	if b.maxTokens > 0 {
		chatReq.MaxTokens = openai.Int(int64(b.maxTokens))
	}

	temp := cfg.Temperature
	if temp != 0 {
		chatReq.Temperature = openai.Float(temp)
	}
	if cfg.Forced {
		logger.Info(
			b.providerName+" temperature adjusted for model constraints",
			"provider", b.providerName,
			"modelType", b.modelType,
			"configuredTemperature", b.temperature,
			"requestTemperature", temp,
		)
	}

	chatResp, err := b.client.Chat.Completions.New(ctx, chatReq, cfg.RequestOpts...)
	if err != nil {
		logger.Error(b.providerName+" request send error", "provider", b.providerName, "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		logger.Error(b.providerName+" no choices", "provider", b.providerName)
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
	reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
	rawMessage := choice.Message.RawJSON()

	reasoningFn := cfg.ReasoningFn
	if reasoningFn == nil {
		reasoningFn = extractReasoningText
	}
	reasoningText := reasoningFn(rawMessage)
	finalContent := choice.Message.Content
	finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, b.providerName, toolCalls)

	logger.Info(
		b.providerName+" response",
		"provider", b.providerName,
		"modelType", b.modelType,
		"modelName", b.modelName,
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

	resp := &Response{
		Content:          finalContent,
		ReasoningContent: reasoningText,
		ToolCalls:        toolCalls,
		Usage: Usage{
			PromptTokens:     int(chatResp.Usage.PromptTokens),
			CompletionTokens: int(chatResp.Usage.CompletionTokens),
			TotalTokens:      int(chatResp.Usage.TotalTokens),
		},
	}

	if cfg.ExtraReasoningDetails {
		resp.ReasoningDetails = extractReasoningDetails(rawMessage)
	}

	return resp, nil
}
