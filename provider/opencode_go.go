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

const openCodeGoAPIBase = "https://opencode.ai/zen/go/v1"

func init() {
	base := []string{"glm-5.3-flash", "glm-5.3", "deepseek-v4.1-flash", "deepseek-v4-pro"}
	models := append([]string(nil), base...)
	vision := []string{}
	windows := map[string]int{}
	// Keep Go's advertised efforts separate from the native DeepSeek
	// route's larger enum; Pro only advertises high/max on this route.
	for _, model := range base {
		efforts := []string{"low", "high", "max"}
		if model == "deepseek-v4-pro" {
			efforts = []string{"high", "max"}
		}
		for _, effort := range efforts {
			models = append(models, model+"["+effort+"]")
		}
	}
	for _, model := range models {
		windows[model] = 1000000
		name, _ := parseModelEffort(model)
		if name == "glm-5.3-flash" || name == "deepseek-v4.1-flash" {
			vision = append(vision, model)
		}
	}
	RegisterProvider("opencode-go", ProviderRegistration{
		Models: models, VisionModels: vision, ContextWindows: windows,
		EnvKey: "OPENCODE_GO_API_KEY", EnvBase: "OPENCODE_GO_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newOpenCodeGoProvider(apiKey, apiBase, modelType, modelName, maxTokens)
		},
	})
}

// OpenCodeGoProvider uses Go's Chat Completions endpoint for GLM/DeepSeek.
// Other Go models require separate Messages or Responses adapters.
type OpenCodeGoProvider struct {
	client            openai.Client
	modelType         string
	modelName         string
	effort            string
	maxTokens         int
	fallbackSessionID string
}

func newOpenCodeGoProvider(apiKey, apiBase, modelType, modelName string, maxTokens int) *OpenCodeGoProvider {
	if modelName == "" {
		modelName = modelType
	}
	modelName, _ = parseModelEffort(modelName)
	_, effort := parseModelEffort(modelType)
	return &OpenCodeGoProvider{
		client: openai.NewClient(
			oaioption.WithAPIKey(apiKey),
			oaioption.WithBaseURL(normalizeSDKBaseURL(apiBase, openCodeGoAPIBase, "/chat/completions")),
			oaioption.WithHeader("User-Agent", "nagobot/1.0"),
			oaioption.WithMaxRetries(sdkMaxRetries),
		),
		modelType: modelType, modelName: modelName, effort: effort, maxTokens: maxTokens,
		// Standalone calls have no session context. Still identify the logical
		// conversation consistently for every request using this instance.
		fallbackSessionID: randomHex(16),
	}
}

func (p *OpenCodeGoProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	messages, err := toOpenAIChatMessages(req.Messages, SupportsVision("opencode-go", p.modelType), false, false)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}
	if strings.HasPrefix(p.modelName, "deepseek-") {
		// DeepSeek requires the key on every assistant message, even when
		// historical thinking is empty or was trimmed before this request.
		for _, message := range messages {
			if assistant := message.OfAssistant; assistant != nil {
				extras := assistant.ExtraFields()
				if extras == nil {
					extras = map[string]any{}
				}
				if _, ok := extras["reasoning_content"]; !ok {
					extras["reasoning_content"] = ""
				}
				assistant.SetExtraFields(extras)
			}
		}
	}
	params := openai.ChatCompletionNewParams{
		Model: shared.ChatModel(p.modelName), Messages: messages, Tools: toOpenAIChatTools(req.Tools),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)},
	}
	if p.maxTokens > 0 {
		params.MaxTokens = openai.Int(int64(p.maxTokens))
	}
	sessionID := sessionCacheKey(ctx)
	if sessionID == "" {
		sessionID = p.fallbackSessionID
	}
	opts := []oaioption.RequestOption{
		oaioption.WithHeader("x-opencode-session", sessionID),
	}
	if strings.HasPrefix(p.modelName, "glm-") {
		// Go routes through multiple upstreams. Use its OpenAI-compatible
		// parameters, not native GLM's thinking/clear_thinking extensions:
		// strict upstreams reject the whole request when thinking is present.
		params.Temperature = openai.Float(1)
	}
	if p.effort != "" {
		opts = append(opts, oaioption.WithJSONSet("reasoning_effort", p.effort))
	}
	start := time.Now()
	logger.Info("opencode-go request", "provider", "opencode-go", "modelType", p.modelType,
		"modelName", p.modelName, "reasoningEffort", p.effort, "toolCount", len(req.Tools))
	resp := &Response{ProviderLabel: "opencode-go", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)
	go func() {
		defer adapter.Finish()
		defer adapter.cancel()
		// Use the adapter's cancellable context so Cancel stops the HTTP stream.
		completion, reasoning, _, _, err := openAIStreamChat(adapter.ctx, p.client, params, adapter, opts...)
		if err != nil {
			adapter.SetError(fmt.Errorf("opencode-go request failed: %w", err))
			return
		}
		if len(completion.Choices) == 0 {
			adapter.SetError(fmt.Errorf("opencode-go: no choices in response"))
			return
		}
		choice := completion.Choices[0]
		resp.ToolCalls = fromOpenAIChatToolCalls(choice.Message.ToolCalls)
		resp.ReasoningContent = extractReasoningText(choice.Message.RawJSON())
		if resp.ReasoningContent == "" {
			resp.ReasoningContent = reasoning
		}
		resp.Content = resolveContentWithReasoningFallback(choice.Message.Content, resp.ReasoningContent, "opencode-go", resp.ToolCalls)
		resp.Usage = openCodeGoUsage(completion.Usage)
		logger.Info("opencode-go response", "provider", "opencode-go", "modelName", p.modelName,
			"finishReason", choice.FinishReason, "toolCallCount", len(resp.ToolCalls),
			"promptTokens", resp.Usage.PromptTokens, "completionTokens", resp.Usage.CompletionTokens,
			"cachedTokens", resp.Usage.CachedTokens, "reasoningTokens", resp.Usage.ReasoningTokens,
			"latencyMs", time.Since(start).Milliseconds())
	}()
	return adapter.Result(), nil
}

func openCodeGoUsage(usage openai.CompletionUsage) Usage {
	cached := usage.PromptTokensDetails.CachedTokens
	if cached == 0 {
		// Some DeepSeek upstreams retain their native cache usage field.
		_ = json.Unmarshal([]byte(usage.JSON.ExtraFields["prompt_cache_hit_tokens"].Raw()), &cached)
	}
	return Usage{
		PromptTokens: int(usage.PromptTokens), CompletionTokens: int(usage.CompletionTokens),
		TotalTokens: int(usage.TotalTokens), CachedTokens: int(cached),
		ReasoningTokens: int(usage.CompletionTokensDetails.ReasoningTokens),
	}
}
