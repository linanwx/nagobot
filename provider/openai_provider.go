package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const openAIAPIBase = "https://api.openai.com/v1"

// longContextPriceBreak caps the gpt-5.6 and gpt-6 families at the price break
// instead of at their real capacity (372K for gpt-5.6, up to 500K for
// gpt-6.1). OpenAI bills a request whose input exceeds 272K at long-context
// rates (2x input / 1.5x output on gpt-5.6), for the *whole* request, not just
// the excess. The context window is what drives compression and the context
// budget trim (Tier 2 at 70%, Tier 3 at 85%, trim at min(92%,
// window-maxTokens-10K)), so registering the true capacity would let requests
// grow past 272K, with every request in that band silently billed at the
// higher rate. 272000 caps the trim line at ~245K, making crossing the band
// structurally impossible. This is the same defect Codex itself has:
// openai/codex#32486.
//
// The registered window is only ever consumed as a budget (compression
// thresholds and Runner.contextBudget); nagobot never surfaces a model's
// native capacity, so collapsing capacity and cap into one value is safe.
const longContextPriceBreak = 272000

// Reasoning effort tiers, selectable per model via a bracket suffix:
// "gpt-6-luna[low]". The two longer lists mirror supported_reasoning_levels
// from GET /v1/models on the subscription route; a model is registered with
// exactly the tiers OpenAI says it accepts.
var (
	effortsToXHigh = []string{"low", "medium", "high", "xhigh"}
	effortsToMax   = []string{"low", "medium", "high", "xhigh", "max"}
	effortsToUltra = []string{"low", "medium", "high", "xhigh", "max", "ultra"}
)

// parseModelEffort splits a bracketed model name ("gpt-5.6-sol[low]") into
// the base API model ("gpt-5.6-sol") and the reasoning effort ("low"). Names
// without a bracket return effort "" (the provider default applies).
func parseModelEffort(name string) (base, effort string) {
	open := strings.IndexByte(name, '[')
	if open == -1 || !strings.HasSuffix(name, "]") {
		return name, ""
	}
	return name[:open], name[open+1 : len(name)-1]
}

// openAIModel is one registered base model: its effort tiers (nil = no
// bracket variants) and its context window.
type openAIModel struct {
	name    string
	efforts []string
	window  int
}

// registerOpenAIModels expands each base model into itself plus one
// "model[effort]" entry per tier, so the whitelist / vision / context-window
// registries treat each effort tier as a first-class model type.
func registerOpenAIModels(name string, models []openAIModel, reg ProviderRegistration) {
	reg.ContextWindows = map[string]int{}
	for _, m := range models {
		reg.Models = append(reg.Models, m.name)
		reg.ContextWindows[m.name] = m.window
		for _, e := range m.efforts {
			variant := m.name + "[" + e + "]"
			reg.Models = append(reg.Models, variant)
			reg.ContextWindows[variant] = m.window
		}
	}
	reg.VisionModels = reg.Models
	RegisterProvider(name, reg)
}

// gpt6Models is the GPT-6 family as the subscription route's model catalog
// lists it (visibility "list", supported_in_api). Every one takes text and
// image input.
var gpt6Models = []openAIModel{
	{"gpt-6.1-sol", effortsToUltra, longContextPriceBreak},
	{"gpt-6.1-terra", effortsToUltra, longContextPriceBreak},
	{"gpt-6-sol", effortsToUltra, longContextPriceBreak},
	{"gpt-6-astra", effortsToUltra, longContextPriceBreak},
	{"gpt-6-luna", effortsToMax, longContextPriceBreak},
}

func init() {
	// "openai": API key auth against api.openai.com. Context windows reflect
	// each model's full API capacity, except for the families with a
	// long-context price break (see longContextPriceBreak).
	openAIModels := append([]openAIModel{
		{"gpt-5.6-sol", effortsToXHigh, longContextPriceBreak},
		{"gpt-5.6-terra", effortsToXHigh, longContextPriceBreak},
		{"gpt-5.6-luna", effortsToXHigh, longContextPriceBreak},
		{"gpt-5.5", nil, 1048576},
		{"gpt-5.4", nil, 1048576},
		{"gpt-5.4-mini", nil, 400000},
		{"gpt-5.4-nano", nil, 200000},
	}, gpt6Models...)
	registerOpenAIModels("openai", openAIModels, ProviderRegistration{
		EnvKey:  "OPENAI_API_KEY",
		EnvBase: "OPENAI_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newOpenAIProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})

	// "openai-oauth": a ChatGPT subscription shared through "Sign in with
	// ChatGPT" (nagobot auth openai), calling the same api.openai.com
	// Responses endpoint with the user's access token. The model list is the
	// subscription catalog (GET /v1/models with that token): gpt-5.5 and
	// gpt-5.4 are not offered on this route. The API base is fixed; an
	// OPENAI_API_BASE or providers.openai.apiBase meant for the API-key
	// provider must not redirect a subscription token elsewhere.
	oauthModels := append(append([]openAIModel{}, gpt6Models...),
		openAIModel{"gpt-5.6-sol", effortsToUltra, longContextPriceBreak},
		openAIModel{"gpt-5.6-terra", effortsToUltra, longContextPriceBreak},
		openAIModel{"gpt-5.6-luna", effortsToMax, longContextPriceBreak},
	)
	registerOpenAIModels("openai-oauth", oauthModels, ProviderRegistration{
		Constructor: func(apiKey, _, modelType, modelName string, maxTokens int, temperature float64) Provider {
			p := newOpenAIProvider(apiKey, openAIAPIBase, modelType, modelName, maxTokens, temperature)
			p.subscription = true
			return p
		},
	})
}

// OpenAIProvider implements the Provider interface using the OpenAI Responses API.
type OpenAIProvider struct {
	apiKey      string
	baseURL     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	httpClient  *http.Client

	// subscription marks the openai-oauth provider: the bearer is a ChatGPT
	// subscription token, not an API key. The route rejects temperature
	// (400 "Unsupported parameter") and only caches prompts for requests that
	// carry the session affinity headers (see doRequest).
	subscription bool
}

func newOpenAIProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *OpenAIProvider {
	if modelName == "" {
		modelName = modelType
	}
	baseURL := strings.TrimSpace(apiBase)
	if baseURL == "" {
		baseURL = openAIAPIBase
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &OpenAIProvider{
		apiKey:      apiKey,
		baseURL:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// Chat sends one full-context request over HTTP/SSE. There is no
// previous_response_id chaining: the subscription route requires store=false,
// so the server keeps nothing to chain from, and a stable prompt_cache_key plus
// the session affinity headers already make the repeated prefix a cache hit.
func (p *OpenAIProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	providerLabel := "openai"
	if p.subscription {
		providerLabel = "openai-oauth"
	}

	logger.Info(
		"openai request",
		"provider", providerLabel,
		"modelType", p.modelType,
		"modelName", p.modelName,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	resp := &Response{ProviderLabel: providerLabel, ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()
		p.runHTTPStream(ctx, req, adapter, resp, start)
	}()

	return adapter.Result(), nil
}

// runHTTPStream performs one HTTP request/SSE cycle, feeding results into the
// already-returned adapter.
func (p *OpenAIProvider) runHTTPStream(ctx context.Context, req *Request, adapter *streamAdapter, resp *Response, start time.Time) {
	body, err := p.buildRequestBody(ctx, req)
	if err != nil {
		adapter.SetError(fmt.Errorf("failed to build request: %w", err))
		return
	}
	marshaled, err := json.Marshal(body)
	if err != nil {
		adapter.SetError(fmt.Errorf("failed to encode request: %w", err))
		return
	}

	httpResp, err := p.doRequest(ctx, p.baseURL+"/responses", marshaled)
	if err != nil {
		logger.Error("openai request error", "provider", resp.ProviderLabel, "err", err)
		adapter.SetError(fmt.Errorf("request failed: %w", err))
		return
	}
	if httpResp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		logger.Error("openai request error", "provider", resp.ProviderLabel, "status", httpResp.StatusCode, "body", string(errBody))
		if code, msg := responsesErrorCode(errBody); isSubscriptionErrorCode(code) {
			adapter.SetError(subscriptionError(code, msg))
			return
		}
		adapter.SetError(fmt.Errorf("request failed: %d %s", httpResp.StatusCode, string(errBody)))
		return
	}

	perr := parseSSEStream(httpResp, adapter)
	httpResp.Body.Close()
	if perr != nil {
		logger.Error("openai SSE parse error", "provider", resp.ProviderLabel, "err", perr)
		adapter.SetError(perr)
		return
	}

	logger.Info(
		"openai response",
		"provider", resp.ProviderLabel,
		"modelType", p.modelType,
		"modelName", p.modelName,
		"hasToolCalls", len(resp.ToolCalls) > 0,
		"toolCallCount", len(resp.ToolCalls),
		"promptTokens", resp.Usage.PromptTokens,
		"completionTokens", resp.Usage.CompletionTokens,
		"reasoningTokens", resp.Usage.ReasoningTokens,
		"cachedTokens", resp.Usage.CachedTokens,
		"cacheWriteTokens", resp.Usage.CacheWriteTokens,
		"totalTokens", resp.Usage.TotalTokens,
		"outputChars", len(resp.Content),
		"latencyMs", time.Since(start).Milliseconds(),
	)
}

// doRequest issues one POST to the Responses API with the given pre-built body.
//
// On the subscription route the three affinity headers are what make prompt
// caching work at all: measured on gpt-6-luna, the same 14K-token prefix sent
// twice cached 14208/14418 tokens with them and 0 without, prompt_cache_key
// alone notwithstanding. They carry the same hashed session key as
// prompt_cache_key, so one session lands on one cache shard across turns.
func (p *OpenAIProvider) doRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	if p.subscription {
		if key := sessionCacheKey(ctx); key != "" {
			httpReq.Header.Set("session-id", key)
			httpReq.Header.Set("thread-id", key)
			httpReq.Header.Set("x-client-request-id", key)
		}
	}
	return p.httpClient.Do(httpReq)
}

// userMessageToInputItems converts a single user-role message to Responses API
// input items (always exactly one "message" item).
func userMessageToInputItems(msg Message) []map[string]any {
	content := []map[string]any{
		{"type": "input_text", "text": msg.Content},
	}
	if len(msg.Media) > 0 {
		_, markers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
		for _, marker := range markers {
			if !strings.HasPrefix(marker.MimeType, "image/") {
				continue // OpenAI Responses API only supports image media
			}
			b64, err := ReadFileAsBase64(marker.FilePath)
			if err != nil {
				continue
			}
			content = append(content, map[string]any{
				"type":      "input_image",
				"image_url": "data:" + marker.MimeType + ";base64," + b64,
			})
		}
	}
	return []map[string]any{{
		"type":    "message",
		"role":    "user",
		"content": content,
	}}
}

// assistantMessageToInputItems converts a single assistant-role message to
// Responses API input items: an optional output_text message, reasoning items
// (unless trimmed), then one function_call item per tool call.
func assistantMessageToInputItems(msg Message) []map[string]any {
	var input []map[string]any
	if msg.Content != "" {
		input = append(input, map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": msg.Content},
			},
		})
	}
	// Insert OpenAI reasoning items before function_calls if not trimmed.
	// Only include items with type="reasoning" — ReasoningDetails from other
	// providers (Anthropic thinking blocks, Gemini thought_signature) have
	// different formats and must be skipped.
	if !msg.ReasoningTrimmed && len(msg.ReasoningDetails) > 0 {
		var items []json.RawMessage
		if err := json.Unmarshal(msg.ReasoningDetails, &items); err == nil {
			for _, raw := range items {
				var ri map[string]any
				if err := json.Unmarshal(raw, &ri); err == nil {
					if riType, _ := ri["type"].(string); riType == "reasoning" {
						input = append(input, ri)
					}
				}
			}
		}
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if !json.Valid([]byte(args)) {
			args = "{}"
		}
		input = append(input, map[string]any{
			"type":      "function_call",
			"call_id":   tc.ID,
			"name":      tc.Function.Name,
			"arguments": args,
		})
	}
	return input
}

// toolMessageToInputItems converts a single tool-result message to Responses
// API input items (always exactly one function_call_output item).
func toolMessageToInputItems(msg Message) []map[string]any {
	cleanedText, markers := ParseMediaMarkers(msg.Content)
	hasMedia := len(markers) > 0
	output := []map[string]any{
		{"type": "input_text", "text": cleanedText},
	}
	for _, marker := range markers {
		if !strings.HasPrefix(marker.MimeType, "image/") {
			continue // OpenAI only supports image media
		}
		b64, err := ReadFileAsBase64(marker.FilePath)
		if err != nil {
			continue
		}
		output = append(output, map[string]any{
			"type":      "input_image",
			"image_url": "data:" + marker.MimeType + ";base64," + b64,
		})
	}
	// Process explicit media attachments.
	if len(msg.Media) > 0 {
		_, mediaMarkers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
		for _, marker := range mediaMarkers {
			if !strings.HasPrefix(marker.MimeType, "image/") {
				continue // OpenAI Responses API only supports image media
			}
			b64, err := ReadFileAsBase64(marker.FilePath)
			if err != nil {
				continue
			}
			output = append(output, map[string]any{
				"type":      "input_image",
				"image_url": "data:" + marker.MimeType + ";base64," + b64,
			})
			hasMedia = true
		}
	}
	if hasMedia {
		return []map[string]any{{
			"type":    "function_call_output",
			"call_id": msg.ToolCallID,
			"output":  output,
		}}
	}
	return []map[string]any{{
		"type":    "function_call_output",
		"call_id": msg.ToolCallID,
		"output":  msg.Content,
	}}
}

// convertMessagesToInputItems converts a run of messages to Responses API
// input items. System messages are skipped (handled separately as
// instructions) — callers that need instructions must scan for them too.
func convertMessagesToInputItems(messages []Message) []map[string]any {
	var input []map[string]any
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			input = append(input, userMessageToInputItems(msg)...)
		case "assistant":
			input = append(input, assistantMessageToInputItems(msg)...)
		case "tool":
			input = append(input, toolMessageToInputItems(msg)...)
		}
	}
	return input
}

// buildRequestBody converts internal Request to Responses API JSON.
//
// ctx supplies the session key (via provider.WithSessionKey) used to derive
// prompt_cache_key, stable per session so calls across turns route to the
// same server-side prompt cache shard.
func (p *OpenAIProvider) buildRequestBody(ctx context.Context, req *Request) (map[string]any, error) {
	var instructions []string
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			instructions = append(instructions, msg.Content)
		}
	}
	joinedInstructions := strings.Join(instructions, "\n\n")

	// Convert tools to Responses API format (flat structure).
	var tools []map[string]any
	for _, t := range req.Tools {
		tool := map[string]any{
			"type":       "function",
			"name":       t.Function.Name,
			"parameters": t.Function.Parameters,
		}
		if t.Function.Description != "" {
			tool["description"] = t.Function.Description
		}
		tools = append(tools, tool)
	}

	// A bracket suffix on the model name ("gpt-5.6-sol[low]") selects the
	// reasoning effort explicitly. Without one, default to high, except the
	// sol tier, which burns tokens too fast at high effort for everyday use.
	apiModel, effort := parseModelEffort(p.modelName)
	if effort == "" {
		effort = "high"
		if strings.HasSuffix(apiModel, "-sol") {
			effort = "medium"
		}
	}

	body := map[string]any{
		"model":   apiModel,
		"input":   convertMessagesToInputItems(req.Messages),
		"stream":  true,
		"store":   false,
		"include": []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{
			"effort":  effort,
			"summary": "auto",
		},
	}
	if cacheKey := sessionCacheKey(ctx); cacheKey != "" {
		body["prompt_cache_key"] = cacheKey
	}
	if apiModel == "gpt-5.4" || apiModel == "gpt-5.5" {
		body["text"] = map[string]any{"verbosity": "low"}
	}
	if joinedInstructions != "" {
		body["instructions"] = joinedInstructions
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	if p.maxTokens > 0 {
		body["max_output_tokens"] = p.maxTokens
	}
	// The subscription route rejects temperature outright (400 "Unsupported
	// parameter: temperature"), and so do the mini/nano models.
	noTemp := p.subscription || apiModel == "gpt-5.4-mini" || apiModel == "gpt-5.4-nano"
	if p.temperature != 0 && !noTemp {
		body["temperature"] = p.temperature
	}
	return body, nil
}

// parseSSEStream reads a Responses API SSE event stream and assembles the
// complete response into the adapter's Response, emitting deltas as they
// arrive.
func parseSSEStream(httpResp *http.Response, adapter *streamAdapter) error {
	asm := newResponseAssembler(adapter)

	scanner := bufio.NewScanner(httpResp.Body)
	// Increase buffer for large events.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {json}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		done, evErr := asm.handleEvent([]byte(data))
		if evErr != nil {
			return evErr
		}
		if done {
			break
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return fmt.Errorf("reading SSE stream: %w", scanErr)
	}

	asm.finish()
	return nil
}

// responseAssembler processes the Responses API's streaming event vocabulary
// (response.output_text.delta, response.output_item.done,
// response.completed/done/incomplete, response.failed).
type responseAssembler struct {
	adapter *streamAdapter
	resp    *Response

	content          strings.Builder
	reasoning        strings.Builder
	reasoningItems   []json.RawMessage
	toolCalls        []ToolCall
	toolCallSignaled bool
}

func newResponseAssembler(adapter *streamAdapter) *responseAssembler {
	return &responseAssembler{adapter: adapter, resp: adapter.resp}
}

// handleEvent processes one decoded event payload. done=true means the
// response has completed (successfully, or with response.incomplete — still
// a valid result to process, not an error); err set means response.failed or
// another unrecoverable condition that must abort the turn.
func (a *responseAssembler) handleEvent(data []byte) (done bool, err error) {
	var event struct {
		Type     string         `json:"type"`
		Delta    string         `json:"delta,omitempty"`
		Item     map[string]any `json:"item,omitempty"`
		Response struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
				TotalTokens  int `json:"total_tokens"`
				// Kept raw so the exact keys the server sent can be logged when
				// a details object fails to parse.
				InputTokensDetails  json.RawMessage `json:"input_tokens_details"`
				OutputTokensDetails struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
			Error *responsesAPIError `json:"error,omitempty"`
		} `json:"response,omitempty"`
	}
	if unmarshalErr := json.Unmarshal(data, &event); unmarshalErr != nil {
		return false, nil // skip unparseable events
	}

	switch event.Type {
	case "response.output_text.delta":
		a.adapter.EmitText(event.Delta)

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		// Live reasoning stream (for UI display only). The authoritative
		// ReasoningContent still comes from the completed reasoning item in
		// extractOutputItem — these deltas never touch a.reasoning.
		a.adapter.EmitReasoning(event.Delta)

	case "response.output_item.done":
		if itemType, _ := event.Item["type"].(string); itemType == "function_call" {
			if !a.toolCallSignaled {
				a.toolCallSignaled = true
				name, _ := event.Item["name"].(string)
				a.adapter.EmitToolCall(name)
			}
		}
		a.extractOutputItem(event.Item)

	case "response.completed", "response.done", "response.incomplete":
		var inDetails struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		}
		rawDetails := event.Response.Usage.InputTokensDetails
		if len(rawDetails) > 0 {
			if detErr := json.Unmarshal(rawDetails, &inDetails); detErr != nil {
				logger.Warn("openai usage: input_tokens_details failed to parse",
					"err", detErr, "raw", string(rawDetails))
			}
		}
		a.resp.Usage = Usage{
			PromptTokens:     event.Response.Usage.InputTokens,
			CompletionTokens: event.Response.Usage.OutputTokens,
			TotalTokens:      event.Response.Usage.TotalTokens,
			CachedTokens:     inDetails.CachedTokens,
			CacheWriteTokens: inDetails.CacheWriteTokens,
			ReasoningTokens:  event.Response.Usage.OutputTokensDetails.ReasoningTokens,
		}

		return true, nil

	case "response.failed":
		errInfo := event.Response.Error
		if errInfo != nil {
			if isSubscriptionErrorCode(errInfo.Code) {
				return false, subscriptionError(errInfo.Code, errInfo.Message)
			}
			return false, fmt.Errorf("API error [%s]: %s", errInfo.Code, errInfo.Message)
		}
		return false, fmt.Errorf("API returned response.failed")
	}
	return false, nil
}

// extractOutputItem processes a single completed output item from the stream.
func (a *responseAssembler) extractOutputItem(item map[string]any) {
	if item == nil {
		return
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		contentArr, _ := item["content"].([]any)
		for _, c := range contentArr {
			block, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType == "output_text" {
				if text, _ := block["text"].(string); text != "" {
					if a.content.Len() > 0 {
						a.content.WriteString("\n")
					}
					a.content.WriteString(text)
				}
			}
		}

	case "function_call":
		callID, _ := item["call_id"].(string)
		name, _ := item["name"].(string)
		args, _ := item["arguments"].(string)
		a.toolCalls = append(a.toolCalls, ToolCall{
			ID:   callID,
			Type: "function",
			Function: FunctionCall{
				Name:      name,
				Arguments: args,
			},
		})

	case "reasoning":
		// Extract summary text from reasoning item.
		if summaryArr, ok := item["summary"].([]any); ok {
			for _, s := range summaryArr {
				block, ok := s.(map[string]any)
				if !ok {
					continue
				}
				if blockType, _ := block["type"].(string); blockType == "summary_text" {
					if text, _ := block["text"].(string); text != "" {
						if a.reasoning.Len() > 0 {
							a.reasoning.WriteString("\n")
						}
						a.reasoning.WriteString(text)
					}
				}
			}
		}
		// Preserve the complete reasoning item as raw JSON for round-trip.
		if raw, err := json.Marshal(item); err == nil {
			a.reasoningItems = append(a.reasoningItems, json.RawMessage(raw))
		}
	}
}

// finish flushes the assembled content/reasoning/tool calls into the
// adapter's Response. Called once after the event loop ends successfully —
// NOT called on error paths, matching the original single-function behavior.
func (a *responseAssembler) finish() {
	if len(a.reasoningItems) > 0 {
		a.resp.ReasoningDetails, _ = json.Marshal(a.reasoningItems)
	}
	a.resp.Content = a.content.String()
	a.resp.ReasoningContent = a.reasoning.String()
	a.resp.ToolCalls = a.toolCalls
}

type responsesAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Subscription error codes the subscription route returns, either as an HTTP
// error body or as response.failed after the stream has started. Each is a
// statement about the account, not the request, so retrying cannot help; the
// message says what the person has to do instead.
var subscriptionErrorHints = map[string]string{
	"subscription_sharing_usage_limit_exceeded":   "the ChatGPT subscription's usage limit is reached; it resets on the plan's own schedule",
	"subscription_sharing_user_not_eligible":      "this ChatGPT account's plan cannot share its subscription with third-party apps",
	"subscription_sharing_usage_unavailable":      "OpenAI could not check the subscription's usage right now; try again later",
	"subscription_sharing_unsupported_capability": "the request used a capability the subscription route does not support",
	"subscription_sharing_route_not_supported":    "the subscription route does not support this endpoint or model",
	"subscription_sharing_invalid_user":           "the signed-in ChatGPT user is not valid; run 'nagobot auth openai' to sign in again",
	"subscription_sharing_user_unavailable":       "the signed-in ChatGPT user is unavailable; run 'nagobot auth openai' to sign in again",
	"chatpass_v2_scope_not_authorized":            "the sign-in did not grant subscription access; run 'nagobot auth openai' to sign in again",
	"chatpass_v2_invalid_authorization_context":   "the subscription authorization is no longer valid; run 'nagobot auth openai' to sign in again",
}

func isSubscriptionErrorCode(code string) bool {
	_, ok := subscriptionErrorHints[code]
	return ok
}

// subscriptionError turns a subscription error code into an error a person
// can act on, keeping the server's own message for the details.
func subscriptionError(code, message string) error {
	hint := subscriptionErrorHints[code]
	if message == "" {
		return fmt.Errorf("openai-oauth: %s [%s]", hint, code)
	}
	return fmt.Errorf("openai-oauth: %s [%s: %s]", hint, code, message)
}

// responsesErrorCode extracts the error code and message from a Responses API
// error body: {"error":{"code":"…","message":"…"}}. Returns empty strings when
// the body has another shape.
func responsesErrorCode(body []byte) (code, message string) {
	var parsed struct {
		Error *responsesAPIError `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil || parsed.Error == nil {
		return "", ""
	}
	return parsed.Error.Code, parsed.Error.Message
}

// sessionCacheKey derives a stable per-session identifier from the session
// key carried in ctx (set by thread.executeRunner via WithSessionKey). Used
// as the request's prompt_cache_key and, on the subscription route, as the
// session affinity headers: a value that is stable across turns keeps this
// session's requests on the same prompt cache shard, so each new turn's first
// call can hit the previous turn's cached prefix. Hashing rather than sending
// the raw key keeps channel/user ids out of requests. Returns "" when ctx
// carries no session key (e.g. standalone scripts).
//
// A dispatched child keys on its root session (cacheRootSessionKey), not on
// itself. Subagents of one session share the same tools + system prefix, but
// each child key used to hash to its own shard, so a sibling's first call
// missed a prefix cached minutes earlier (measured on kingsley: one hit, one
// miss across the two in-TTL pairs).
func sessionCacheKey(ctx context.Context) string {
	key := cacheRootSessionKey(SessionKeyFromContext(ctx))
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

// cacheRootSessionKey strips every child segment from a session key, so
// "discord:1:threads:a:fork:b" becomes "discord:1". The infixes mirror
// session.ThreadsSessionInfix and session.ForkSessionInfix (session imports
// provider, so they cannot be referenced here). Sibling suffixes such as
// ":progresssummary" are left alone: those run a different agent, so they
// share no prefix with the parent worth routing together.
func cacheRootSessionKey(key string) string {
	for _, infix := range []string{":threads:", ":fork:"} {
		if i := strings.Index(key, infix); i > 0 {
			key = key[:i]
		}
	}
	return key
}
