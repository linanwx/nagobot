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

// glm-5.3 is text-only; glm-5.3-flash is natively multimodal (image / video /
// file) and is the only GLM registered as vision-capable. Video and file input
// have no marker in this codebase, so only images actually reach it — the
// registration claims exactly what the media pipeline can deliver.
func init() {
	RegisterProvider("zhipu-cn", ProviderRegistration{
		Models:       []string{"glm-5.3", "glm-5.3-flash"},
		VisionModels: []string{"glm-5.3-flash"},
		ContextWindows: map[string]int{
			"glm-5.3":       1000000,
			"glm-5.3-flash": 1000000,
		},
		EnvKey:  "ZHIPU_API_KEY",
		EnvBase: "ZHIPU_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newZhipuProvider("zhipu-cn", apiKey, apiBase, zhipuCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("zhipu-global", ProviderRegistration{
		Models:       []string{"glm-5.3", "glm-5.3-flash"},
		VisionModels: []string{"glm-5.3-flash"},
		ContextWindows: map[string]int{
			"glm-5.3":       1000000,
			"glm-5.3-flash": 1000000,
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
	providerName string
	apiKey       string
	apiBase      string
	modelName    string
	modelType    string
	maxTokens    int
	temperature  float64
	client       openai.Client
}

// zhipuThinkingEnabled reports whether the model runs with thinking on.
//
// Every GLM-5.3 model does, unconditionally: "enabled" is the ONLY value
// thinking.type accepts, and "disabled" is a 400 ("该模型始终思考，不支持关闭
// 思考"). So this never varies today — it is kept as a predicate because it
// also decides the forced temperature below, and because the next GLM may
// bring the switch back.
func zhipuThinkingEnabled(modelType string) bool {
	switch strings.TrimSpace(modelType) {
	case "glm-5.3", "glm-5.3-flash":
		return true
	}
	return false
}

// zhipuReasoningEffort returns the reasoning_effort value to send.
//
// The dial is real and it moves: measured against open.bigmodel.cn, the same
// arithmetic question produced 32 / ~90 / ~135 characters of reasoning at low /
// unset / max, identically on both models. The legal set is exactly
// {low, high, max} — none, minimal, medium and xhigh are all 400 on this
// family, even though the error message lists them, because each of them would
// mean "think less than low" or names a tier GLM does not have.
//
// glm-5.3-flash takes the same dial as glm-5.3, so both send "high": the flash
// model is cheaper per token, not shallower per thought, and picking its depth
// is a routing decision (which specialty points at it), not a property of the
// model id.
func zhipuReasoningEffort(modelType string) string {
	if zhipuThinkingEnabled(modelType) {
		return "high"
	}
	return ""
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
func (p *ZhipuProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, SupportsVision(p.providerName, p.modelType), false, false)
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
		logger.Info(
			"zhipu temperature adjusted for thinking constraints",
			"provider", p.providerName,
			"modelType", p.modelType,
			"configuredTemperature", p.temperature,
			"requestTemperature", requestTemp,
		)
	}

	// Both fields are TOP-LEVEL, and that is the whole point of this block.
	// They used to be sent under an "extra_body" wrapper, which is a Python-SDK
	// convention the client unwraps before the request goes out — not part of
	// the wire protocol. open.bigmodel.cn simply ignores an unknown top-level
	// object, so the wrapper turned both settings into dead weight and returned
	// 200 the whole time: verified by sending extra_body.thinking.type
	// "disabled" (200, still thinks) next to a top-level "disabled" (400), and
	// by sending an unknown field of the same shape, which behaves identically.
	// A silently-ignored parameter is the worst outcome available here — the
	// request looks configured and is not.
	requestOpts := []oaioption.RequestOption{}
	if thinkingEnabled {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("thinking.type", "enabled"),
		)
	}
	if effort := zhipuReasoningEffort(p.modelType); effort != "" {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("reasoning_effort", effort),
		)
	}

	resp := &Response{ProviderLabel: p.providerName, ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()

		chatResp, streamReasoning, _, _, err := openAIStreamChat(ctx, p.client, chatReq, adapter, requestOpts...)
		if err != nil {
			logger.Error("zhipu request send error", "provider", p.providerName, "err", err)
			adapter.SetError(fmt.Errorf("request failed: %w", err))
			return
		}

		if len(chatResp.Choices) == 0 {
			logger.Error("zhipu no choices", "provider", p.providerName)
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
		finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "zhipu", toolCalls)

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
