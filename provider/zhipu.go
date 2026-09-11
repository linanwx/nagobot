// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"fmt"
	"slices"
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

const (
	glm53Model      = "glm-5.3"
	glm53FlashModel = "glm-5.3-flash"
	glm53Window     = 1000000
)

// zhipuReasoningEfforts is the vendor's own enum, and the ORDER IS THE TRAP:
// low < high < max, with max the DEFAULT that an absent field selects. So
// "high" is the middle tier, not the top one. docs.bigmodel.cn states both
// halves outright: it glosses max as "default and recommended, deep reasoning",
// high as "enhanced reasoning" and low as "light reasoning", and specifies
// `reasoning_effort: {default: max, enum: [max, high, low]}`. Anything outside
// the three is a 400 on GLM-5.3 / GLM-5.3-Flash.
//
// Tiers are selectable per model via a bracket suffix ("glm-5.3-flash[low]"),
// the same shape openai and deepseek use (see parseModelEffort). A bare alias
// sends NO reasoning_effort and gets the vendor default.
var zhipuReasoningEfforts = []string{"low", "high", "max"}

// zhipuWithEffortVariants appends "model[effort]" entries for every base model
// so the whitelist, vision and context-window registries treat each tier as a
// first-class model type.
//
// Vision in particular is keyed on the nagobot-facing modelType, bracket and
// all (visionCapable[provider+":"+modelType]), so a variant left out of
// VisionModels is not a missing tier — it is a model that silently drops every
// image with no error anywhere.
func zhipuWithEffortVariants(models []string, base ...string) []string {
	for _, m := range base {
		for _, e := range zhipuReasoningEfforts {
			models = append(models, m+"["+e+"]")
		}
	}
	return models
}

// glm-5.3 is text-only; glm-5.3-flash is natively multimodal (image / video /
// file) and is the only GLM registered as vision-capable. Video and file input
// have no marker in this codebase, so only images actually reach it — the
// registration claims exactly what the media pipeline can deliver.
func init() {
	base := []string{glm53Model, glm53FlashModel}
	models := zhipuWithEffortVariants(slices.Clone(base), base...)
	vision := zhipuWithEffortVariants([]string{glm53FlashModel}, glm53FlashModel)
	windows := map[string]int{}
	for _, m := range models {
		windows[m] = glm53Window
	}

	RegisterProvider("zhipu-cn", ProviderRegistration{
		Models:         models,
		VisionModels:   vision,
		ContextWindows: windows,
		EnvKey:         "ZHIPU_API_KEY",
		EnvBase:        "ZHIPU_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newZhipuProvider("zhipu-cn", apiKey, apiBase, zhipuCNAPIBase, modelType, modelName, maxTokens, temperature)
		},
	})

	RegisterProvider("zhipu-global", ProviderRegistration{
		Models:         models,
		VisionModels:   vision,
		ContextWindows: windows,
		EnvKey:         "ZHIPU_GLOBAL_API_KEY",
		EnvBase:        "ZHIPU_GLOBAL_API_BASE",
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
	modelName    string // wire model name sent to Zhipu (bracket stripped)
	modelType    string // nagobot-facing alias (may carry an [effort] bracket)
	maxTokens    int
	temperature  float64
	effort       string // tier from an [effort] bracket; "" = vendor default (max)
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
	// Strip the [effort] bracket first: it is part of the nagobot-facing alias,
	// not of the model's identity, and an unmatched name here would turn
	// thinking off AND release the forced temperature on a model that requires
	// both.
	base, _ := parseModelEffort(strings.TrimSpace(modelType))
	switch base {
	case glm53Model, glm53FlashModel:
		return true
	}
	return false
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
	// The tier rides on modelType — that is the name a routing rule is written
	// with — while modelName is what goes on the wire, where a bracket is a 400.
	// Stripping both independently keeps an explicit thread.modelName override
	// from smuggling one through.
	modelName, _ = parseModelEffort(modelName)
	_, effort := parseModelEffort(strings.TrimSpace(modelType))
	if !slices.Contains(zhipuReasoningEfforts, effort) {
		effort = "" // no bracket, or a tier this family rejects: vendor default
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
		effort:       effort,
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
		"reasoningEffort", p.effort,
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

	// TOP-LEVEL, not under an "extra_body" wrapper. That wrapper is a Python-SDK
	// convention the client unwraps before the request goes out; on the wire it
	// is an unknown object this endpoint ignores, so anything inside it was
	// silently dropped while the request still returned 200.
	//
	// reasoning_effort is sent ONLY when a bracket suffix asked for a tier. A
	// bare alias omits the field and takes the vendor default (max), which is
	// the deepest setting — see zhipuReasoningEfforts for why the enum's order
	// is a trap.
	//
	// This file shipped a hard-coded "high" from 2026-08-26 to 2026-09-11, and
	// what that cost is worth recording, because the answer-only measurements
	// that justified it hid the real damage. Measured against the live endpoint
	// on glm-5.3-flash, n=5, streaming, identical request, reasoning tokens:
	//
	//	turn shape                 high        field omitted
	//	answers directly        median  781    median 2989
	//	emits a tool call       median   53    median 1931
	//
	// So the penalty is ~4x when the model is answering and ~36x when it is
	// about to call a tool — and in an agentic loop the second row is the
	// common case (69% of this deployment's GLM turns carried tool_calls).
	// glm-5.3 behaves the same (88-107 against 1407-2991).
	//
	// Downstream, a tool-calling turn with no deliberation left the model
	// improvising one: 47 turns wrote their own chain of thought into
	// exec `echo "<reasoning prose>"`, read it back as tool output, and
	// answered from that. All 47 were glm-5.3-flash — zero across ~3000 exec
	// calls from the other 19 models sharing the same prompt and tools — and
	// all 46 of the prose-length ones sat on a turn whose reasoning_tokens was
	// 0. The saving was ~2000 output tokens per call; each induced round trip
	// re-sent ~152,000 prompt tokens.
	//
	// clear_thinking:false opts into Preserved Thinking, which the vendor says
	// requires passing historical reasoning_content back complete and in order
	// — toOpenAIChatMessages does (extras["reasoning_content"]). The docs also
	// scope it explicitly: it affects only the historical thinking blocks
	// carried ACROSS turns, and does not change whether the model produces or
	// emits thinking within the current one. So it cannot deepen this turn, and
	// a head-to-head over 10 turns found exactly that null (7/10 zero-reasoning
	// with it false against 6/10 default). It is kept for cross-turn
	// continuity, not for depth.
	requestOpts := []oaioption.RequestOption{}
	if thinkingEnabled {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("thinking.type", "enabled"),
			oaioption.WithJSONSet("thinking.clear_thinking", false),
		)
		if p.effort != "" {
			requestOpts = append(requestOpts, oaioption.WithJSONSet("reasoning_effort", p.effort))
		}
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
