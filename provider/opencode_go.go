package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
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
			oaioption.WithMiddleware(openCodeGoStreamMiddleware),
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
		// Use Go's OpenAI-compatible parameters. Native GLM's thinking
		// extensions cause some requests to be rejected by this endpoint.
		params.Temperature = openai.Float(1)
	}
	if p.effort != "" {
		opts = append(opts, oaioption.WithJSONSet("reasoning_effort", p.effort))
	}
	start := time.Now()
	requestID := randomHex(8)
	sessionKey := SessionKeyFromContext(ctx)
	logger.Info("opencode-go request", "provider", "opencode-go", "modelType", p.modelType,
		"modelName", p.modelName, "reasoningEffort", p.effort, "toolCount", len(req.Tools),
		"requestID", requestID, "sessionKey", sessionKey)
	resp := &Response{ProviderLabel: "opencode-go", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)
	go func() {
		defer adapter.Finish()
		defer adapter.cancel()
		// Use the adapter's cancellable context so Cancel stops the HTTP stream.
		completion, reasoning, _, _, err := openAIStreamChat(adapter.ctx, p.client, params, adapter, opts...)
		if err != nil {
			logger.Warn("opencode-go stream failed", "requestID", requestID, "sessionKey", sessionKey,
				"modelName", p.modelName, "latencyMs", time.Since(start).Milliseconds(), "err", err)
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
			"requestID", requestID, "sessionKey", sessionKey,
			"finishReason", choice.FinishReason, "toolCallCount", len(resp.ToolCalls),
			"promptTokens", resp.Usage.PromptTokens, "completionTokens", resp.Usage.CompletionTokens,
			"cachedTokens", resp.Usage.CachedTokens, "reasoningTokens", resp.Usage.ReasoningTokens,
			"latencyMs", time.Since(start).Milliseconds())
	}()
	return adapter.Result(), nil
}

// The SDK decodes every SSE event as JSON, including comment-only keep-alives.
// Filter only events without non-empty data; preserve all payloads verbatim so
// malformed JSON and upstream error events still fail rather than being hidden.
func openCodeGoStreamMiddleware(req *http.Request, next oaioption.MiddlewareNext) (*http.Response, error) {
	resp, err := next(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && mediaType == "text/event-stream" {
		resp.Body = &openCodeGoSSEBody{body: resp.Body, reader: bufio.NewReader(resp.Body)}
	}
	return resp, nil
}

type openCodeGoSSEBody struct {
	body    io.ReadCloser
	reader  *bufio.Reader
	pending []byte
	err     error
}

func (r *openCodeGoSSEBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		var event bytes.Buffer
		hasData := false
		for {
			line, err := r.reader.ReadBytes('\n')
			event.Write(line)
			if bytes.HasPrefix(line, []byte("data:")) && len(bytes.TrimSpace(line[5:])) > 0 {
				hasData = true
			}
			if err != nil {
				r.err = err
				break
			}
			if bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n")) {
				break
			}
		}
		if hasData {
			r.pending = event.Bytes()
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *openCodeGoSSEBody) Close() error { return r.body.Close() }

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
