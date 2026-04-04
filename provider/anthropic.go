// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	aoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/linanwx/nagobot/logger"
)

const (
	anthropicAPIBase = "https://api.anthropic.com"
)

// anthropicRateLimitCache stores the latest rate-limit headers from Anthropic responses.
// Package-level because AnthropicProvider instances are recreated per request (hot-reload).
var (
	anthropicRateLimitMu    sync.RWMutex
	anthropicRateLimitCache *AnthropicRateLimits
)

// AnthropicRateLimits holds cached rate-limit header values.
type AnthropicRateLimits struct {
	RequestsLimit     int       `json:"requests_limit"`
	RequestsRemaining int       `json:"requests_remaining"`
	TokensLimit       int       `json:"tokens_limit"`
	TokensRemaining   int       `json:"tokens_remaining"`
	InputLimit        int       `json:"input_limit"`
	InputRemaining    int       `json:"input_remaining"`
	OutputLimit       int       `json:"output_limit"`
	OutputRemaining   int       `json:"output_remaining"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// GetAnthropicRateLimits returns the latest cached rate-limit info, or nil.
func GetAnthropicRateLimits() *AnthropicRateLimits {
	anthropicRateLimitMu.RLock()
	defer anthropicRateLimitMu.RUnlock()
	if anthropicRateLimitCache == nil {
		return nil
	}
	cp := *anthropicRateLimitCache
	return &cp
}

func anthropicRateLimitMiddleware(req *http.Request, next aoption.MiddlewareNext) (*http.Response, error) {
	resp, err := next(req)
	if err != nil || resp == nil {
		return resp, err
	}
	// Extract rate-limit headers.
	rl := &AnthropicRateLimits{UpdatedAt: time.Now()}
	rl.RequestsLimit, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-requests-limit"))
	rl.RequestsRemaining, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-requests-remaining"))
	rl.TokensLimit, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-tokens-limit"))
	rl.TokensRemaining, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-tokens-remaining"))
	rl.InputLimit, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-input-tokens-limit"))
	rl.InputRemaining, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-input-tokens-remaining"))
	rl.OutputLimit, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-output-tokens-limit"))
	rl.OutputRemaining, _ = strconv.Atoi(resp.Header.Get("anthropic-ratelimit-output-tokens-remaining"))
	// Only cache if we got meaningful data (headers present on /v1/messages responses).
	if rl.RequestsLimit > 0 || rl.TokensLimit > 0 {
		anthropicRateLimitMu.Lock()
		anthropicRateLimitCache = rl
		anthropicRateLimitMu.Unlock()
	}
	return resp, err
}

func init() {
	RegisterProvider("anthropic", ProviderRegistration{
		Models:       []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"},
		VisionModels: []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"},
		PDFModels:    []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"},
		ContextWindows: map[string]int{
			"claude-sonnet-4-6": 1048576,
			"claude-opus-4-6":   1048576,
			"claude-haiku-4-5":  200000,
		},
		EnvKey:  "ANTHROPIC_API_KEY",
		EnvBase: "ANTHROPIC_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newAnthropicProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// AnthropicProvider implements the Provider interface for Anthropic.
type AnthropicProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      anthropic.Client
}

const (
	anthropicThinkingMinBudget     = 1024
	anthropicThinkingDefaultBudget = 2048
)

func anthropicThinkingEnabled(modelType string) bool {
	switch strings.TrimSpace(modelType) {
	case "claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5":
		return true
	default:
		return false
	}
}

func anthropicRequestTemperature(thinkingEnabled bool, configured float64) (float64, bool) {
	if thinkingEnabled {
		return 1, configured != 1
	}
	return configured, false
}

func anthropicThinkingBudget(maxTokens int) (int64, bool) {
	if maxTokens <= anthropicThinkingMinBudget {
		return 0, false
	}

	budget := anthropicThinkingDefaultBudget
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget < anthropicThinkingMinBudget {
		return 0, false
	}
	return int64(budget), true
}

// newAnthropicProvider creates a new Anthropic provider.
func newAnthropicProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *AnthropicProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, anthropicAPIBase, "/v1/messages")

	client := anthropic.NewClient(
		aoption.WithAPIKey(apiKey),
		aoption.WithBaseURL(baseURL),
		aoption.WithMaxRetries(sdkMaxRetries),
		aoption.WithMiddleware(anthropicRateLimitMiddleware),
	)

	return &AnthropicProvider{
		apiKey:      apiKey,
		apiBase:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      client,
	}
}

func anthropicInputChars(systemPrompt string, messages []Message) int {
	total := len(systemPrompt)
	for _, m := range messages {
		if m.Role != "system" {
			total += len(m.Content)
		}
	}
	return total
}

// anthropicThinkingDetail represents a single thinking or redacted_thinking block
// for round-tripping Anthropic thinking content across multi-turn conversations.
type anthropicThinkingDetail struct {
	Type      string `json:"type"`                // "thinking" or "redacted_thinking"
	Thinking  string `json:"thinking,omitempty"`   // thinking text (for type "thinking")
	Signature string `json:"signature,omitempty"`  // opaque signature (for type "thinking")
	Data      string `json:"data,omitempty"`       // opaque data (for type "redacted_thinking")
}

// anthropicThinkingBlocks reconstructs thinking content blocks from a Message's
// ReasoningDetails for round-tripping back to the Anthropic API.
func anthropicThinkingBlocks(m Message) []anthropic.ContentBlockParamUnion {
	if len(m.ReasoningDetails) == 0 {
		return nil
	}
	var details []anthropicThinkingDetail
	if err := json.Unmarshal(m.ReasoningDetails, &details); err != nil || len(details) == 0 {
		return nil
	}
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(details))
	for _, d := range details {
		switch d.Type {
		case "thinking":
			if d.Thinking != "" && d.Signature != "" {
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfThinking: &anthropic.ThinkingBlockParam{
						Thinking:  d.Thinking,
						Signature: d.Signature,
					},
				})
			}
		case "redacted_thinking":
			if d.Data != "" {
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfRedactedThinking: &anthropic.RedactedThinkingBlockParam{
						Data: d.Data,
					},
				})
			}
		}
	}
	if len(blocks) > 0 {
		return blocks
	}
	return nil
}

func parseFunctionArguments(arguments string) any {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}
	}

	var parsed any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return map[string]any{} // invalid JSON → empty object (safe for API)
	}
	return parsed
}

func normalizeRequiredSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

func toAnthropicTools(tools []ToolDef) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schema := anthropic.ToolInputSchemaParam{ExtraFields: map[string]any{}}
		if params := t.Function.Parameters; params != nil {
			if properties, ok := params["properties"]; ok {
				schema.Properties = properties
			}
			if required, ok := params["required"]; ok {
				schema.Required = normalizeRequiredSlice(required)
			}
			for k, v := range params {
				if k == "type" || k == "properties" || k == "required" {
					continue
				}
				schema.ExtraFields[k] = v
			}
		}

		tool := anthropic.ToolParam{
			Name:        t.Function.Name,
			InputSchema: schema,
		}
		if t.Function.Description != "" {
			tool.Description = anthropic.String(t.Function.Description)
		}

		result = append(result, anthropic.ToolUnionParam{OfTool: &tool})
	}
	// Set cache_control on the last tool to cache the full tools + system prefix.
	if n := len(result); n > 0 {
		if t := result[n-1].OfTool; t != nil {
			t.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
	}
	return result
}

func toAnthropicMessages(messages []Message) (string, []anthropic.MessageParam, error) {
	var systemPrompt string
	msgList := make([]anthropic.MessageParam, 0, len(messages))

	// Anthropic expects tool results to be in a user message.
	pendingToolResults := make([]anthropic.ContentBlockParamUnion, 0)

	flushPendingToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		msgList = append(msgList, anthropic.NewUserMessage(pendingToolResults...))
		pendingToolResults = nil
	}

	for _, m := range messages {
		switch m.Role {
		case "system":
			systemPrompt = m.Content
		case "user":
			flushPendingToolResults()
			if len(m.Media) > 0 {
				_, markers := ParseMediaMarkers(strings.Join(m.Media, "\n"))
				blocks := []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)}
				for _, marker := range markers {
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					if marker.MimeType == "application/pdf" {
						blocks = append(blocks, anthropic.ContentBlockParamUnion{
							OfDocument: &anthropic.DocumentBlockParam{
								Source: anthropic.DocumentBlockParamSourceUnion{
									OfBase64: &anthropic.Base64PDFSourceParam{
										Data: b64,
									},
								},
							},
						})
						continue
					}
					if !strings.HasPrefix(marker.MimeType, "image/") {
						continue // Anthropic only supports image and PDF media
					}
					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfImage: &anthropic.ImageBlockParam{
							Source: anthropic.ImageBlockParamSourceUnion{
								OfBase64: &anthropic.Base64ImageSourceParam{
									MediaType: anthropic.Base64ImageSourceMediaType(marker.MimeType),
									Data:      b64,
								},
							},
						},
					})
				}
				msgList = append(msgList, anthropic.NewUserMessage(blocks...))
			} else {
				msgList = append(msgList, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
			}
		case "assistant":
			flushPendingToolResults()

			blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
			// Thinking blocks must come before text (Anthropic requires this order).
			blocks = append(blocks, anthropicThinkingBlocks(m)...)
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				if tc.Type != "" && tc.Type != "function" {
					return "", nil, fmt.Errorf("unsupported assistant tool call type: %s", tc.Type)
				}
				blocks = append(blocks, anthropic.ContentBlockParamUnion{OfToolUse: &anthropic.ToolUseBlockParam{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: parseFunctionArguments(tc.Function.Arguments),
				}})
			}
			if len(blocks) > 0 {
				msgList = append(msgList, anthropic.NewAssistantMessage(blocks...))
			}
		case "tool":
			cleanedText, markers := ParseMediaMarkers(m.Content)
			var content []anthropic.ToolResultBlockParamContentUnion
			if cleanedText != "" {
				content = append(content, anthropic.ToolResultBlockParamContentUnion{
					OfText: &anthropic.TextBlockParam{Text: cleanedText},
				})
			}
			for _, marker := range markers {
				b64, err := ReadFileAsBase64(marker.FilePath)
				if err != nil {
					continue // File missing or unreadable; skip silently.
				}
				if marker.MimeType == "application/pdf" {
					content = append(content, anthropic.ToolResultBlockParamContentUnion{
						OfDocument: &anthropic.DocumentBlockParam{
							Source: anthropic.DocumentBlockParamSourceUnion{
								OfBase64: &anthropic.Base64PDFSourceParam{
									Data: b64,
								},
							},
						},
					})
					continue
				}
				content = append(content, anthropic.ToolResultBlockParamContentUnion{
					OfImage: &anthropic.ImageBlockParam{
						Source: anthropic.ImageBlockParamSourceUnion{
							OfBase64: &anthropic.Base64ImageSourceParam{
								MediaType: anthropic.Base64ImageSourceMediaType(marker.MimeType),
								Data:      b64,
							},
						},
					},
				})
			}
			// Process explicit media attachments.
			if len(m.Media) > 0 {
				_, mediaMarkers := ParseMediaMarkers(strings.Join(m.Media, "\n"))
				for _, marker := range mediaMarkers {
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					if marker.MimeType == "application/pdf" {
						content = append(content, anthropic.ToolResultBlockParamContentUnion{
							OfDocument: &anthropic.DocumentBlockParam{
								Source: anthropic.DocumentBlockParamSourceUnion{
									OfBase64: &anthropic.Base64PDFSourceParam{
										Data: b64,
									},
								},
							},
						})
						continue
					}
					if !strings.HasPrefix(marker.MimeType, "image/") {
						continue // Anthropic only supports image and PDF media
					}
					content = append(content, anthropic.ToolResultBlockParamContentUnion{
						OfImage: &anthropic.ImageBlockParam{
							Source: anthropic.ImageBlockParamSourceUnion{
								OfBase64: &anthropic.Base64ImageSourceParam{
									MediaType: anthropic.Base64ImageSourceMediaType(marker.MimeType),
									Data:      b64,
								},
							},
						},
					})
				}
			}
			if len(content) == 0 {
				content = append(content, anthropic.ToolResultBlockParamContentUnion{
					OfText: &anthropic.TextBlockParam{Text: "(empty)"},
				})
			}
			pendingToolResults = append(pendingToolResults, anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
				ToolUseID: m.ToolCallID,
				Content:   content,
			}})
		default:
			return "", nil, fmt.Errorf("unsupported message role: %s", m.Role)
		}
	}

	flushPendingToolResults()
	return systemPrompt, msgList, nil
}

// Chat sends a chat completion request to Anthropic.
func (p *AnthropicProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()

	systemPrompt, messages, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}
	inputChars := anthropicInputChars(systemPrompt, req.Messages)
	tools := toAnthropicTools(req.Tools)
	thinkingEnabled := anthropicThinkingEnabled(p.modelType)

	logger.Info(
		"anthropic request",
		"provider", "anthropic",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"thinkingEnabled", thinkingEnabled,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	maxTokens := p.maxTokens
	if maxTokens <= 0 {
		maxTokens = anthropicFallbackMaxTokens
	}
	if thinkingEnabled && maxTokens <= anthropicThinkingMinBudget {
		logger.Warn(
			"anthropic max_tokens adjusted for thinking constraints",
			"provider", "anthropic",
			"modelType", p.modelType,
			"configuredMaxTokens", p.maxTokens,
			"requestMaxTokens", anthropicThinkingDefaultBudget,
		)
		maxTokens = anthropicThinkingDefaultBudget
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.modelName),
		MaxTokens: int64(maxTokens),
		Messages:  messages,
		Tools:     tools,
	}
	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{
			Text:         systemPrompt,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
	}
	if thinkingEnabled {
		if budget, ok := anthropicThinkingBudget(maxTokens); ok {
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
		} else {
			logger.Warn(
				"anthropic thinking disabled due to invalid max_tokens budget constraints",
				"provider", "anthropic",
				"modelType", p.modelType,
				"requestMaxTokens", maxTokens,
			)
		}
	}
	requestTemp, forcedTemp := anthropicRequestTemperature(thinkingEnabled, p.temperature)
	if requestTemp != 0 {
		params.Temperature = anthropic.Float(requestTemp)
	}
	if forcedTemp {
		logger.Info(
			"anthropic temperature adjusted for thinking constraints",
			"provider", "anthropic",
			"modelType", p.modelType,
			"configuredTemperature", p.temperature,
			"requestTemperature", requestTemp,
		)
	}

	resp := &Response{ProviderLabel: "anthropic", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()

		stream := p.client.Messages.NewStreaming(ctx, params)
		var textParts []string
		var toolCallSignaled bool
		var reasoningParts []string
		var thinkingDetails []anthropicThinkingDetail
		var toolCalls []ToolCall
		var promptTokens, completionTokens, cacheCreationTokens, cacheReadTokens int64
		var stopReason string

		// Per-block accumulators, keyed by block index.
		type blockState struct {
			blockType string
			id, name  string          // tool_use
			thinking  strings.Builder // thinking
			signature strings.Builder // thinking signature
			data      string          // redacted_thinking
			args      strings.Builder // tool_use input_json
			text      strings.Builder // text
		}
		blocks := make(map[int64]*blockState)

		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "message_start":
				promptTokens = event.Message.Usage.InputTokens
				cacheCreationTokens = event.Message.Usage.CacheCreationInputTokens
				cacheReadTokens = event.Message.Usage.CacheReadInputTokens

			case "content_block_start":
				bs := &blockState{blockType: event.ContentBlock.Type}
				switch event.ContentBlock.Type {
				case "tool_use":
					bs.id = event.ContentBlock.ID
					bs.name = event.ContentBlock.Name
					if !toolCallSignaled {
						toolCallSignaled = true
						adapter.EmitToolCall(event.ContentBlock.Name)
					}
				case "redacted_thinking":
					bs.data = event.ContentBlock.Data
				}
				blocks[event.Index] = bs

			case "content_block_delta":
				bs := blocks[event.Index]
				if bs == nil {
					continue
				}
				switch event.Delta.Type {
				case "text_delta":
					bs.text.WriteString(event.Delta.Text)
					adapter.EmitText(event.Delta.Text)
				case "thinking_delta":
					bs.thinking.WriteString(event.Delta.Thinking)
				case "signature_delta":
					bs.signature.WriteString(event.Delta.Signature)
				case "input_json_delta":
					bs.args.WriteString(event.Delta.PartialJSON)
				}

			case "content_block_stop":
				bs := blocks[event.Index]
				if bs == nil {
					continue
				}
				switch bs.blockType {
				case "text":
					if t := bs.text.String(); t != "" {
						textParts = append(textParts, t)
					}
				case "thinking":
					thinking := bs.thinking.String()
					if strings.TrimSpace(thinking) != "" {
						reasoningParts = append(reasoningParts, strings.TrimSpace(thinking))
					}
					thinkingDetails = append(thinkingDetails, anthropicThinkingDetail{
						Type:      "thinking",
						Thinking:  thinking,
						Signature: bs.signature.String(),
					})
				case "redacted_thinking":
					reasoningParts = append(reasoningParts, "[redacted_thinking]")
					thinkingDetails = append(thinkingDetails, anthropicThinkingDetail{
						Type: "redacted_thinking",
						Data: bs.data,
					})
				case "tool_use":
					toolCalls = append(toolCalls, ToolCall{
						ID:   bs.id,
						Type: "function",
						Function: FunctionCall{
							Name:      bs.name,
							Arguments: bs.args.String(),
						},
					})
				}

			case "message_delta":
				completionTokens = int64(event.Usage.OutputTokens)
				stopReason = string(event.Delta.StopReason)
			}
		}
		if err := stream.Err(); err != nil {
			logger.Error("anthropic stream error", "provider", "anthropic", "err", err)
			adapter.SetError(fmt.Errorf("request failed: %w", err))
		}

		content := strings.Join(textParts, "\n")
		reasoningContent := strings.Join(reasoningParts, "\n")
		var reasoningDetailsJSON json.RawMessage
		if len(thinkingDetails) > 0 {
			if data, err := json.Marshal(thinkingDetails); err == nil {
				reasoningDetailsJSON = data
			}
		}

		totalInput := int(promptTokens + cacheCreationTokens + cacheReadTokens)
		logger.Info(
			"anthropic response",
			"provider", "anthropic",
			"modelType", p.modelType,
			"modelName", p.modelName,
			"finishReason", stopReason,
			"reasoningInResponse", strings.TrimSpace(reasoningContent) != "",
			"hasToolCalls", len(toolCalls) > 0,
			"toolCallCount", len(toolCalls),
			"promptTokens", promptTokens,
			"completionTokens", completionTokens,
			"cacheCreationTokens", cacheCreationTokens,
			"cacheReadTokens", cacheReadTokens,
			"totalTokens", promptTokens+cacheCreationTokens+cacheReadTokens+completionTokens,
			"outputChars", len(content),
			"latencyMs", time.Since(start).Milliseconds(),
		)

		resp.Content = content
		resp.ReasoningContent = reasoningContent
		resp.ReasoningDetails = reasoningDetailsJSON
		resp.ToolCalls = toolCalls
		resp.Usage = Usage{
			PromptTokens:     totalInput,
			CompletionTokens: int(completionTokens),
			TotalTokens:      totalInput + int(completionTokens),
			CachedTokens:     int(cacheReadTokens),
		}
	}()

	return adapter.Result(), nil
}
