package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const deepSeekAPIBase = "https://api.deepseek.com"

// instantSuffix marks DeepSeek model aliases that disable thinking mode.
// E.g. "deepseek-flash-instant" wires to "deepseek-flash" with thinking off.
const instantSuffix = "-instant"

// dsFlashModel is DeepSeek-V4.1-Flash, released 2026-09-10. The wire id carries
// no version at all — "deepseek-flash", not "deepseek-v4.1-flash" (OpenRouter
// spells the same model the other way; see openrouter.go, and do not assume one
// id works on both routes).
//
// It replaces BOTH retired predecessors with one model: deepseek-v4-flash and
// deepseek-v4-flash-vision-exp are gone, and DeepSeek routes those two names
// here "temporarily", with no announced end date. It is natively multimodal, so
// there is no longer a separate vision id to register — 1M context, 384K max
// output, and 4.4x cheaper input / 3.3x cheaper output than deepseek-v4-pro.
const dsFlashModel = "deepseek-flash"

// dsReasoningEfforts is the FULL set the API accepts, read out of the
// deserializer's own rejection message rather than guessed:
//
//	reasoning_effort: unknown variant `bogus`, expected one of
//	`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`
//
// They are selectable per model via a bracket suffix ("deepseek-flash[max]"),
// the same shape openai uses (see parseModelEffort). Omitting the field leaves
// DeepSeek's own default, which sits between "low" and "high".
//
// Measured on deepseek-flash, reasoning tokens on one fixed prompt at n=8:
// minimal mean 1728, medium 3601, max 4222 — a real ~2.4x ladder, but one that
// needs n>=8 to see past the per-call variance. deepseek-v4-pro (n=4) runs
// none 0 / low 1841 / high 4931.
var dsReasoningEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// dsEffortNone is the one tier that cannot be expressed as an effort alongside
// thinking.type. Measured: "thinking":{"type":"enabled"} OVERRIDES a top-level
// "reasoning_effort":"none" (median 3163 reasoning tokens, n=5), while "none"
// sent without a thinking field yields 0 every time (5/5). thinking.type is the
// master switch, so newDeepSeekProvider folds [none] into thinking-off rather
// than shipping an enum value that silently means its opposite.
const dsEffortNone = "none"

// dsWithEffortVariants appends "model[effort]" entries for every base model so
// the whitelist and context-window registries treat each tier as a first-class
// model type. -instant aliases get none: thinking is off, so effort is moot.
func dsWithEffortVariants(models []string, base ...string) []string {
	for _, m := range base {
		for _, e := range dsReasoningEfforts {
			models = append(models, m+"["+e+"]")
		}
	}
	return models
}

func init() {
	base := []string{dsFlashModel, "deepseek-v4-pro"}
	models := []string{
		dsFlashModel, "deepseek-v4-pro",
		dsFlashModel + instantSuffix, "deepseek-v4-pro" + instantSuffix,
	}
	models = dsWithEffortVariants(models, base...)

	windows := map[string]int{}
	for _, m := range models {
		windows[m] = 1000000
	}

	// V4.1-Flash is natively multimodal, so vision is now a property of the
	// mainline model rather than of a separate experimental id. Every alias
	// counts, not just the bare one: SupportsVision is keyed on the
	// nagobot-facing modelType, which still carries the -instant / [effort]
	// suffix when the caller picked one. deepseek-v4-pro has no vision at all
	// (the pricing table says so outright) and must not appear here.
	var visionModels []string
	for _, m := range models {
		if strings.HasPrefix(m, dsFlashModel) {
			visionModels = append(visionModels, m)
		}
	}

	RegisterProvider("deepseek", ProviderRegistration{
		Models:         models,
		VisionModels:   visionModels,
		ContextWindows: windows,
		EnvKey:         "DEEPSEEK_API_KEY",
		EnvBase:        "DEEPSEEK_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newDeepSeekProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// ---------- JSON wire types ----------

type dsRequest struct {
	Model         string        `json:"model"`
	Messages      []dsMessage   `json:"messages"`
	Tools         []ToolDef     `json:"tools,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions *dsStreamOpts `json:"stream_options,omitempty"`
	Thinking      *dsThinking   `json:"thinking,omitempty"`
	// ReasoningEffort is TOP-LEVEL, and that placement is the whole point of
	// this field existing separately from dsThinking. It used to be nested
	// inside "thinking", where the API silently ignored it: a bogus value
	// nested returns 200, the same value at top level returns 400 naming the
	// enum. So every [high]/[max] alias ever shipped ran at the server default
	// while the request looked correct and the tests stayed green — the same
	// shape as zhipu's extra_body defect, and the same reason the guard for it
	// (TestDeepSeekSendsReasoningEffortAtTopLevel) asserts on the marshalled
	// body rather than on the provider's fields.
	//
	// Empty means omit, which is DeepSeek's own default depth.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type dsStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// dsContentPart is one element of a multimodal message body. DeepSeek follows
// the OpenAI content-part shape: {"type":"text"} and {"type":"image_url"} with
// a data: URL. The optional "detail" dial is deliberately not sent.
//
// V4.1-Flash dropped the old vision-exp model's flat 384-token cap and tiles
// instead. Measured prompt_tokens for one image plus a two-token prompt:
// <=512x512 -> 190 (a floor), 1024x1024 -> 658, 1920x1080 -> 974,
// 3000x2000 -> 983. EstimateImageTokens still overestimates against this
// (~2764 for 1920x1080), which is the safe direction for a budget guard, so it
// is left alone — see the note in CLAUDE.md about why a provider-dependent
// estimate is not a local change.
type dsContentPart struct {
	Type     string      `json:"type"`
	Text     string      `json:"text,omitempty"`
	ImageURL *dsImageURL `json:"image_url,omitempty"`
}

type dsImageURL struct {
	URL string `json:"url"`
}

// dsThinking is the master switch, and it outranks ReasoningEffort: sending
// type "enabled" alongside reasoning_effort "none" still thinks. The API also
// accepts "adaptive" (let the model choose), which nagobot does not send —
// every model rule here names a depth on purpose.
type dsThinking struct {
	Type string `json:"type"` // "adaptive" | "enabled" | "disabled"
}

type dsMessage struct {
	Role string `json:"role"`
	// Content is a plain string for text-only messages and a []dsContentPart
	// when images ride along. It stays nullable: an assistant message that only
	// carries tool_calls must send content:null.
	Content          any        `json:"content"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"` // assistant only
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`        // assistant only
	ToolCallID       string     `json:"tool_call_id,omitempty"`      // tool only
	Name             string     `json:"name,omitempty"`
}

// Non-streaming response.
type dsResponse struct {
	Choices []dsChoice `json:"choices"`
	Usage   dsUsage    `json:"usage"`
}

type dsChoice struct {
	Message      dsRespMsg `json:"message"`
	FinishReason string    `json:"finish_reason"`
}

type dsRespMsg struct {
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
}

type dsUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	PromptCacheHitTokens    int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int `json:"prompt_cache_miss_tokens"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// Streaming chunk.
type dsChunk struct {
	Choices []dsChunkChoice `json:"choices"`
	Usage   *dsUsage        `json:"usage"`
}

type dsChunkChoice struct {
	Delta        dsDelta `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type dsDelta struct {
	Content          string      `json:"content"`
	ReasoningContent string      `json:"reasoning_content"`
	ToolCalls        []dsDeltaTC `json:"tool_calls"`
}

type dsDeltaTC struct {
	Index    int           `json:"index"`
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function dsDeltaTCFunc `json:"function"`
}

type dsDeltaTCFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type dsErrorResp struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// streamToolCallAcc accumulates incremental tool call deltas.
type streamToolCallAcc struct {
	id   string
	typ  string
	name string
	args strings.Builder
}

// ---------- provider ----------

// DeepSeekProvider implements the Provider interface using raw HTTP.
type DeepSeekProvider struct {
	apiKey      string
	apiBase     string
	modelName   string // wire model name sent to DeepSeek (suffix stripped)
	modelType   string // nagobot-facing alias (may carry -instant suffix)
	maxTokens   int
	temperature float64
	thinking    bool   // false for -instant aliases; suppresses thinking mode
	effort      string // "high"/"max" from a [bracket] suffix; "" = server default
	client      *http.Client
}

func newDeepSeekProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *DeepSeekProvider {
	if modelName == "" {
		modelName = modelType
	}
	// Strip the [effort] bracket before the -instant check: the two suffixes are
	// mutually exclusive by registration, but parsing in this order keeps a
	// hand-written "deepseek-v4-pro-instant[max]" from silently thinking.
	modelName, effort := parseModelEffort(modelName)
	baseType, _ := parseModelEffort(modelType)
	thinking := !strings.HasSuffix(baseType, instantSuffix)
	modelName = strings.TrimSuffix(modelName, instantSuffix)
	if !slices.Contains(dsReasoningEfforts, effort) {
		effort = "" // unknown tier: fall through to DeepSeek's own default
	}
	// [none] is thinking-OFF, not a depth. Left as an effort it would be
	// overridden by thinking.type "enabled" and the turn would reason anyway,
	// so it folds into exactly the state an -instant alias produces. This is
	// the only enum value that cannot survive as itself.
	if effort == dsEffortNone {
		thinking = false
		effort = ""
	}
	if apiBase == "" {
		apiBase = deepSeekAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")
	apiBase = strings.TrimSuffix(apiBase, "/chat/completions")
	apiBase = strings.TrimSuffix(apiBase, "/v1")

	return &DeepSeekProvider{
		apiKey:      apiKey,
		apiBase:     apiBase,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		thinking:    thinking,
		effort:      effort,
		client:      &http.Client{},
	}
}

func (p *DeepSeekProvider) endpoint() string {
	return p.apiBase + "/chat/completions"
}

// Chat sends a chat completion request to DeepSeek.
func (p *DeepSeekProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)
	// Thinking mode is off for -instant aliases and for [none]. Effort comes
	// from a [bracket] suffix; without one the bare aliases send no
	// reasoning_effort at all and DeepSeek applies its own default depth,
	// measured to sit between "low" and "high".
	thinkingEnabled := p.thinking

	logger.Info(
		"deepseek request",
		"provider", "deepseek",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"thinkingEnabled", thinkingEnabled,
		"reasoningEffort", p.effort,
		"streaming", true,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	dsReq := p.buildRequest(req, thinkingEnabled, true)
	return p.chatStream(ctx, dsReq, start)
}

func (p *DeepSeekProvider) buildRequest(req *Request, thinkingEnabled, streaming bool) dsRequest {
	r := dsRequest{
		Model:    p.modelName,
		Messages: toDSMessages(req.Messages, SupportsVision("deepseek", p.modelType)),
		Tools:    req.Tools,
		Stream:   streaming,
	}
	if p.maxTokens > 0 {
		r.MaxTokens = p.maxTokens
	}
	if p.temperature != 0 && !thinkingEnabled {
		t := p.temperature
		r.Temperature = &t
	}
	if thinkingEnabled {
		r.Thinking = &dsThinking{Type: "enabled"}
		r.ReasoningEffort = p.effort
	} else {
		r.Thinking = &dsThinking{Type: "disabled"}
	}
	if streaming {
		r.StreamOptions = &dsStreamOpts{IncludeUsage: true}
	}
	return r
}

func (p *DeepSeekProvider) doPost(ctx context.Context, body any) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	return p.client.Do(httpReq)
}

// chatSync handles non-streaming completion.
func (p *DeepSeekProvider) chatSync(ctx context.Context, dsReq dsRequest, start time.Time) (ChatResult, error) {
	httpResp, err := p.doPost(ctx, dsReq)
	if err != nil {
		logger.Error("deepseek request error", "provider", "deepseek", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		var apiErr dsErrorResp
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, string(body))
	}

	var resp dsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := resp.Choices[0]
	finalContent := choice.Message.Content
	reasoningText := choice.Message.ReasoningContent
	finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "deepseek", choice.Message.ToolCalls)

	u := resp.Usage
	logger.Info(
		"deepseek response",
		"provider", "deepseek",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", choice.FinishReason,
		"reasoningInResponse", u.CompletionTokensDetails.ReasoningTokens > 0 || strings.TrimSpace(reasoningText) != "",
		"hasToolCalls", len(choice.Message.ToolCalls) > 0,
		"toolCallCount", len(choice.Message.ToolCalls),
		"promptTokens", u.PromptTokens,
		"completionTokens", u.CompletionTokens,
		"reasoningTokens", u.CompletionTokensDetails.ReasoningTokens,
		"totalTokens", u.TotalTokens,
		"promptCacheHitTokens", u.PromptCacheHitTokens,
		"promptCacheMissTokens", u.PromptCacheMissTokens,
		"outputChars", len(finalContent),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return NewBasicResult(&Response{
		Content:          finalContent,
		ReasoningContent: reasoningText,
		ToolCalls:        choice.Message.ToolCalls,
		Usage: Usage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
			CachedTokens:     u.PromptCacheHitTokens,
			ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
		},
		ProviderLabel: "deepseek",
		ModelLabel:    p.modelName,
	}), nil
}

// chatStream handles streaming completion with SSE parsing.
func (p *DeepSeekProvider) chatStream(ctx context.Context, dsReq dsRequest, start time.Time) (ChatResult, error) {
	httpResp, err := p.doPost(ctx, dsReq)
	if err != nil {
		logger.Error("deepseek streaming request error", "provider", "deepseek", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		body, _ := io.ReadAll(httpResp.Body)
		var apiErr dsErrorResp
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, string(body))
	}

	resp := &Response{
		ProviderLabel: "deepseek",
		ModelLabel:    p.modelName,
	}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer httpResp.Body.Close()
		defer adapter.Finish()

		var (
			content          strings.Builder
			reasoning        strings.Builder
			toolCallAcc      = map[int]*streamToolCallAcc{}
			toolCallSignaled bool
			usage            dsUsage
			finishReason     string
		)

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			// Skip SSE comments (: keep-alive), empty lines, retry directives.
			if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "retry:") {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := line[6:]
			if data == "[DONE]" {
				break
			}

			var chunk dsChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				logger.Debug("deepseek stream chunk parse skip", "err", err)
				continue
			}

			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta

			if delta.ReasoningContent != "" {
				reasoning.WriteString(delta.ReasoningContent)
				adapter.EmitReasoning(delta.ReasoningContent)
			}
			if delta.Content != "" {
				content.WriteString(delta.Content)
				adapter.EmitText(delta.Content)
			}

			// Accumulate tool calls by index.
			if len(delta.ToolCalls) > 0 && !toolCallSignaled {
				toolCallSignaled = true
				if name := delta.ToolCalls[0].Function.Name; name != "" {
					adapter.EmitToolCall(name)
				}
			}
			for _, tc := range delta.ToolCalls {
				acc, ok := toolCallAcc[tc.Index]
				if !ok {
					acc = &streamToolCallAcc{}
					toolCallAcc[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Type != "" {
					acc.typ = tc.Type
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}

			if chunk.Choices[0].FinishReason != nil {
				finishReason = *chunk.Choices[0].FinishReason
			}
		}

		if err := scanner.Err(); err != nil {
			logger.Error("deepseek stream read error", "err", err)
			adapter.SetError(fmt.Errorf("stream read error: %w", err))
		}

		// Assemble tool calls from accumulated deltas.
		var toolCalls []ToolCall
		for i := 0; i < len(toolCallAcc); i++ {
			tc := toolCallAcc[i]
			if tc == nil {
				continue
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   tc.id,
				Type: tc.typ,
				Function: FunctionCall{
					Name:      tc.name,
					Arguments: tc.args.String(),
				},
			})
		}

		finalContent := content.String()
		reasoningText := reasoning.String()
		finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "deepseek", toolCalls)

		logger.Info(
			"deepseek streaming response",
			"provider", "deepseek",
			"modelType", p.modelType,
			"modelName", p.modelName,
			"finishReason", finishReason,
			"reasoningInResponse", usage.CompletionTokensDetails.ReasoningTokens > 0 || strings.TrimSpace(reasoningText) != "",
			"hasToolCalls", len(toolCalls) > 0,
			"toolCallCount", len(toolCalls),
			"promptTokens", usage.PromptTokens,
			"completionTokens", usage.CompletionTokens,
			"reasoningTokens", usage.CompletionTokensDetails.ReasoningTokens,
			"totalTokens", usage.TotalTokens,
			"promptCacheHitTokens", usage.PromptCacheHitTokens,
			"promptCacheMissTokens", usage.PromptCacheMissTokens,
			"outputChars", len(finalContent),
			"latencyMs", time.Since(start).Milliseconds(),
		)

		// Fill resp fields before Finish closes the channel.
		resp.Content = finalContent
		resp.ReasoningContent = reasoningText
		resp.ToolCalls = toolCalls
		resp.Usage = Usage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			CachedTokens:     usage.PromptCacheHitTokens,
			ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		}
	}()

	return adapter.Result(), nil
}

// ---------- helpers ----------

// dsImageContent builds a multimodal body for one message, or returns ok=false
// when the message has no attachable image and must stay a plain string.
//
// Only image/* is considered: DeepSeek's vision model takes JPEG/PNG/GIF/WebP
// and has no audio or PDF input at all, so an audio marker must fall through to
// the text path rather than becoming an unsendable part.
//
// Markers are stripped from the text ONLY when their image is actually
// attached. A non-vision model keeps the literal <<media:...>> in its tool
// output on purpose — that path is how it learns the file's name and delegates
// to the imagereader agent.
func dsImageContent(text string, media []string) ([]dsContentPart, bool) {
	cleaned, markers := ParseMediaMarkers(text)
	if len(media) > 0 {
		_, extra := ParseMediaMarkers(strings.Join(media, "\n"))
		markers = append(markers, extra...)
	}

	var parts []dsContentPart
	attached := false
	for _, marker := range markers {
		if !strings.HasPrefix(marker.MimeType, "image/") {
			continue
		}
		b64, err := ReadFileAsBase64(marker.FilePath)
		if err != nil {
			logger.Warn("deepseek image read failed, sending text only",
				"path", marker.FilePath, "err", err)
			continue
		}
		parts = append(parts, dsContentPart{
			Type:     "image_url",
			ImageURL: &dsImageURL{URL: "data:" + marker.MimeType + ";base64," + b64},
		})
		attached = true
	}
	if !attached {
		return nil, false
	}
	if cleaned == "" {
		// The message was nothing but markers; an empty text part would be
		// noise, so send the image on its own.
		return parts, true
	}
	return append([]dsContentPart{{Type: "text", Text: cleaned}}, parts...), true
}

func toDSMessages(messages []Message, visionCapable bool) []dsMessage {
	// DeepSeek V4 wire rules (empirically verified; see /tmp/deepseek-empirical):
	//   - reasoning_content KEY must be present on every assistant message
	//     (else API returns 400: "must be passed back to the API").
	//   - VALUE may be any string — DeepSeek accepts both "" and real text.
	//
	// Policy: pass m.ReasoningContent through as-is. Tier-1 trim (>2h via
	// ApplyCompressedMessage) clears the field to "" for stale messages —
	// fresh messages retain their real reasoning, preserving multi-step
	// coherence across tool-call rounds per V4 docs.
	out := make([]dsMessage, 0, len(messages))
	for _, m := range messages {
		dm := dsMessage{
			Role:       m.Role,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		switch m.Role {
		case "assistant":
			if m.Content != "" {
				dm.Content = m.Content
			}
			rc := m.ReasoningContent // may be "" if trimmed or never had reasoning
			dm.ReasoningContent = &rc
			if len(m.ToolCalls) > 0 {
				tcs := make([]ToolCall, len(m.ToolCalls))
				copy(tcs, m.ToolCalls)
				for j := range tcs {
					if !json.Valid([]byte(tcs[j].Function.Arguments)) {
						tcs[j].Function.Arguments = "{}"
					}
				}
				dm.ToolCalls = tcs
			}
		case "user", "tool":
			// Images are legal in user and tool messages only. Verified against
			// the live API: a system message returns 400 "Image in system
			// message is unsupported" and an assistant message 400 "Image in
			// assistant message is not supported", while a tool message is
			// accepted AND read — so unlike the OpenAI-shaped providers, there
			// is no need to defer tool media into a synthetic user message.
			if visionCapable {
				if parts, ok := dsImageContent(m.Content, m.Media); ok {
					dm.Content = parts
					break
				}
			}
			dm.Content = m.Content
		default: // system
			dm.Content = m.Content
		}
		out = append(out, dm)
	}
	return out
}
