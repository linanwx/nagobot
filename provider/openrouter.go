// Package provider provides LLM provider implementations.
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

func extractReasoningText(rawMessage string) string {
	if rawMessage == "" {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(rawMessage), &payload); err != nil {
		return ""
	}

	for _, key := range []string{"reasoning", "reasoning_content", "thinking", "thinking_content"} {
		v, ok := payload[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			return t
		default:
			b, err := json.Marshal(t)
			if err == nil {
				return string(b)
			}
		}
	}

	// Fallback: extract text from reasoning_details array (Gemini thought_signature responses).
	if details, ok := payload["reasoning_details"]; ok {
		if arr, ok := details.([]any); ok {
			var texts []string
			for _, item := range arr {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
					texts = append(texts, t)
				} else if s, ok := m["summary"].(string); ok && strings.TrimSpace(s) != "" {
					texts = append(texts, s)
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, "\n")
			}
		}
	}

	return ""
}

// extractReasoningDetails extracts the reasoning_details array from a raw message JSON.
// Returns nil if the field is absent, null, or empty.
func extractReasoningDetails(rawMessage string) json.RawMessage {
	if rawMessage == "" {
		return nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawMessage), &payload); err != nil {
		return nil
	}
	details, ok := payload["reasoning_details"]
	if !ok || len(details) == 0 || string(details) == "null" || string(details) == "[]" {
		return nil
	}
	return details
}

const (
	openRouterAPIBase = "https://openrouter.ai/api/v1"
)

// openRouterModelMeta holds per-model OpenRouter request options.
type openRouterModelMeta struct {
	ThinkingOpts  []oaioption.RequestOption // thinking/reasoning mode activation
	ProviderOrder []string                  // preferred upstream provider(s)
}

var openRouterModels = map[string]openRouterModelMeta{
	"moonshotai/kimi-k2.5": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("extra_body.chat_template_kwargs.thinking", true),
		},
		ProviderOrder: []string{"moonshotai"},
	},
	"anthropic/claude-sonnet-4.6": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"anthropic"},
	},
	"anthropic/claude-opus-4.6": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"anthropic"},
	},
	"z-ai/glm-5": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"z-ai"},
	},
	"z-ai/glm-5.1": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"z-ai"},
	},
	"z-ai/glm-5-turbo": {
		ProviderOrder: []string{"z-ai"},
	},
	"minimax/minimax-m2.5": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"minimax/fp8"},
	},
	"minimax/minimax-m2.7": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"minimax/fp8"},
	},
	"qwen/qwen3.5-35b-a3b": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"alibaba"},
	},
	"qwen/qwen3.6-plus:free": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"alibaba"},
	},
	"google/gemini-3-flash-preview": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "medium"}),
		},
		ProviderOrder: []string{"google-ai-studio"},
	},
	"google/gemini-3.1-flash-lite-preview": {
		ProviderOrder: []string{"google-ai-studio"},
	},
	"x-ai/grok-4.1-fast": {
		// Grok 4.1 Fast only supports boolean reasoning toggle, not effort levels.
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"enabled": true}),
		},
		ProviderOrder: []string{"xai"},
	},
	"openai/gpt-5.4-mini": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"openai"},
	},
	"anthropic/claude-haiku-4.5": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"anthropic"},
	},
	"xiaomi/mimo-v2-pro": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
	},
	"xiaomi/mimo-v2-omni": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
	},
}

func init() {
	RegisterProvider("openrouter", ProviderRegistration{
		Models:       []string{"moonshotai/kimi-k2.5", "anthropic/claude-sonnet-4.6", "anthropic/claude-opus-4.6", "anthropic/claude-haiku-4.5", "z-ai/glm-5", "z-ai/glm-5.1", "z-ai/glm-5-turbo", "minimax/minimax-m2.5", "minimax/minimax-m2.7", "qwen/qwen3.5-35b-a3b", "qwen/qwen3.5-flash-02-23", "qwen/qwen3.6-plus:free", "google/gemini-3-flash-preview", "google/gemini-3.1-flash-lite-preview", "x-ai/grok-4.1-fast", "openai/gpt-5.4-mini", "xiaomi/mimo-v2-pro", "xiaomi/mimo-v2-omni"},
		VisionModels: []string{"moonshotai/kimi-k2.5", "anthropic/claude-sonnet-4.6", "anthropic/claude-opus-4.6", "anthropic/claude-haiku-4.5", "qwen/qwen3.5-35b-a3b", "qwen/qwen3.5-flash-02-23", "qwen/qwen3.6-plus:free", "google/gemini-3-flash-preview", "google/gemini-3.1-flash-lite-preview", "x-ai/grok-4.1-fast", "openai/gpt-5.4-mini", "xiaomi/mimo-v2-pro", "xiaomi/mimo-v2-omni"},
		AudioModels:  []string{"google/gemini-3-flash-preview", "google/gemini-3.1-flash-lite-preview", "xiaomi/mimo-v2-omni"},
		PDFModels:    []string{"anthropic/claude-sonnet-4.6", "anthropic/claude-opus-4.6", "anthropic/claude-haiku-4.5", "google/gemini-3-flash-preview", "google/gemini-3.1-flash-lite-preview"},
		ContextWindows: map[string]int{
			"moonshotai/kimi-k2.5":          262144,
			"anthropic/claude-sonnet-4.6":   1048576,
			"anthropic/claude-opus-4.6":     1048576,
			"z-ai/glm-5":                   200000,
			"z-ai/glm-5.1":                 200000,
			"z-ai/glm-5-turbo":             202752,
			"minimax/minimax-m2.5":          196608,
			"minimax/minimax-m2.7":          204800,
			"qwen/qwen3.5-35b-a3b":         262144,
			"qwen/qwen3.5-flash-02-23":     1000000,
			"qwen/qwen3.6-plus:free":       1000000,
			"google/gemini-3-flash-preview":      1048576,
			"google/gemini-3.1-flash-lite-preview": 1048576,
			"x-ai/grok-4.1-fast":                  2000000,
			"openai/gpt-5.4-mini":                 400000,
			"anthropic/claude-haiku-4.5":           200000,
			"xiaomi/mimo-v2-pro":                  1048576,
			"xiaomi/mimo-v2-omni":                 262144,
		},
		EnvKey:  "OPENROUTER_API_KEY",
		EnvBase: "OPENROUTER_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newOpenRouterProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// OpenRouterProvider implements the Provider interface for OpenRouter.
type OpenRouterProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      openai.Client
}

// newOpenRouterProvider creates a new OpenRouter provider.
func newOpenRouterProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *OpenRouterProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, openRouterAPIBase, "/chat/completions")
	client := openai.NewClient(
		oaioption.WithAPIKey(apiKey),
		oaioption.WithBaseURL(baseURL),
		oaioption.WithHeader("HTTP-Referer", "https://github.com/linanwx/nagobot"),
		oaioption.WithHeader("X-Title", "nagobot"),
		oaioption.WithMaxRetries(sdkMaxRetries),
	)

	return &OpenRouterProvider{
		apiKey:      apiKey,
		apiBase:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      client,
	}
}




func toOpenAIChatMessages(messages []Message, visionCapable, audioCapable, pdfCapable bool) ([]openai.ChatCompletionMessageParamUnion, error) {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case "system":
			result = append(result, openai.SystemMessage(m.Content))
		case "user":
			if len(m.Media) > 0 {
				_, markers := ParseMediaMarkers(strings.Join(m.Media, "\n"))
				var parts []openai.ChatCompletionContentPartUnionParam
				parts = append(parts, openai.TextContentPart(m.Content))
				for _, marker := range markers {
					isImage := strings.HasPrefix(marker.MimeType, "image/")
					isAudio := strings.HasPrefix(marker.MimeType, "audio/")
					isPDF := marker.MimeType == "application/pdf"
					if (isImage && !visionCapable) || (isAudio && !audioCapable) || (isPDF && !pdfCapable) {
						continue
					}
					if !isImage && !isAudio && !isPDF {
						continue
					}
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					if isImage || isPDF {
						parts = append(parts, openai.ChatCompletionContentPartUnionParam{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:" + marker.MimeType + ";base64," + b64,
								},
							},
						})
					} else if isAudio {
						ext := strings.TrimPrefix(marker.MimeType, "audio/")
						if ext == "mpeg" {
							ext = "mp3"
						}
						parts = append(parts, openai.InputAudioContentPart(
							openai.ChatCompletionContentPartInputAudioInputAudioParam{
								Data:   b64,
								Format: ext,
							},
						))
					}
				}
				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfArrayOfContentParts: parts,
						},
					},
				})
			} else {
				result = append(result, openai.UserMessage(m.Content))
			}
		case "tool":
			cleanedText, markers := ParseMediaMarkers(m.Content)
			result = append(result, openai.ToolMessage(cleanedText, m.ToolCallID))
			// Chat Completions doesn't support media in tool messages.
			// Inject a synthetic user message with media content as a workaround.
			if len(markers) > 0 {
				var parts []openai.ChatCompletionContentPartUnionParam
				for _, marker := range markers {
					isImage := strings.HasPrefix(marker.MimeType, "image/")
					isAudio := strings.HasPrefix(marker.MimeType, "audio/")
					isPDF := marker.MimeType == "application/pdf"
					if (isImage && !visionCapable) || (isAudio && !audioCapable) || (isPDF && !pdfCapable) {
						continue
					}
					if !isImage && !isAudio && !isPDF {
						continue
					}
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					if isImage || isPDF {
						parts = append(parts, openai.ChatCompletionContentPartUnionParam{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:" + marker.MimeType + ";base64," + b64,
								},
							},
						})
					} else if isAudio {
						// OpenRouter input_audio format.
						ext := strings.TrimPrefix(marker.MimeType, "audio/")
						if ext == "mpeg" {
							ext = "mp3"
						}
						parts = append(parts, openai.InputAudioContentPart(
							openai.ChatCompletionContentPartInputAudioInputAudioParam{
								Data:   b64,
								Format: ext,
							},
						))
					}
				}
				if len(parts) > 0 {
					// Build descriptive hint listing each media file.
					var hint strings.Builder
					hint.WriteString("[Media returned by the tool call above:")
					for _, marker := range markers {
						hint.WriteString("\n- ")
						hint.WriteString(marker.FilePath)
						hint.WriteString(" (")
						hint.WriteString(marker.MimeType)
						hint.WriteString(")")
					}
					hint.WriteString("]")
					parts = append([]openai.ChatCompletionContentPartUnionParam{
						openai.TextContentPart(hint.String()),
					}, parts...)
					result = append(result, openai.ChatCompletionMessageParamUnion{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfArrayOfContentParts: parts,
							},
						},
					})
				}
			}
			// Process explicit media attachments (tool role).
			if len(m.Media) > 0 {
				_, mediaMarkers := ParseMediaMarkers(strings.Join(m.Media, "\n"))
				var mediaParts []openai.ChatCompletionContentPartUnionParam
				for _, marker := range mediaMarkers {
					isImage := strings.HasPrefix(marker.MimeType, "image/")
					isAudio := strings.HasPrefix(marker.MimeType, "audio/")
					isPDF := marker.MimeType == "application/pdf"
					if (isImage && !visionCapable) || (isAudio && !audioCapable) || (isPDF && !pdfCapable) {
						continue
					}
					if !isImage && !isAudio && !isPDF {
						continue
					}
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					if isImage || isPDF {
						mediaParts = append(mediaParts, openai.ChatCompletionContentPartUnionParam{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "data:" + marker.MimeType + ";base64," + b64,
								},
							},
						})
					} else if isAudio {
						ext := strings.TrimPrefix(marker.MimeType, "audio/")
						if ext == "mpeg" {
							ext = "mp3"
						}
						mediaParts = append(mediaParts, openai.InputAudioContentPart(
							openai.ChatCompletionContentPartInputAudioInputAudioParam{
								Data:   b64,
								Format: ext,
							},
						))
					}
				}
				if len(mediaParts) > 0 {
					var hint strings.Builder
					hint.WriteString("[Media returned by the tool call above:")
					for _, marker := range mediaMarkers {
						hint.WriteString("\n- ")
						hint.WriteString(marker.FilePath)
						hint.WriteString(" (")
						hint.WriteString(marker.MimeType)
						hint.WriteString(")")
					}
					hint.WriteString("]")
					mediaParts = append([]openai.ChatCompletionContentPartUnionParam{
						openai.TextContentPart(hint.String()),
					}, mediaParts...)
					result = append(result, openai.ChatCompletionMessageParamUnion{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfArrayOfContentParts: mediaParts,
							},
						},
					})
				}
			}
		case "assistant":
			assistant := openai.ChatCompletionAssistantMessageParam{}
			if contentStr := strings.TrimSpace(m.Content); contentStr != "" {
				assistant.Content.OfString = openai.String(contentStr)
			}
			extras := map[string]any{}
			if reasoningContent := strings.TrimSpace(m.ReasoningContent); reasoningContent != "" {
				extras["reasoning_content"] = reasoningContent
			}
			// NOTE: reasoning_details (containing provider-specific signatures) are
			// intentionally NOT forwarded. OpenRouter may route to different upstream
			// providers between requests (e.g. Anthropic direct → Amazon Bedrock),
			// and thinking-block signatures are only valid for the originating provider.
			if len(extras) > 0 {
				assistant.SetExtraFields(extras)
			}

			if len(m.ToolCalls) > 0 {
				assistant.ToolCalls = make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					if tc.Type != "" && tc.Type != "function" {
						return nil, fmt.Errorf("unsupported assistant tool call type: %s", tc.Type)
					}
					args := tc.Function.Arguments
					if !json.Valid([]byte(args)) {
						args = "{}"
					}
					assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tc.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Function.Name,
								Arguments: args,
							},
						},
					})
				}
			}

			result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
		default:
			return nil, fmt.Errorf("unsupported message role: %s", m.Role)
		}
	}

	return result, nil
}

func toOpenAIChatTools(tools []ToolDef) []openai.ChatCompletionToolUnionParam {
	result := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		functionDef := shared.FunctionDefinitionParam{
			Name:       t.Function.Name,
			Parameters: shared.FunctionParameters(t.Function.Parameters),
		}
		if t.Function.Description != "" {
			functionDef.Description = openai.String(t.Function.Description)
		}

		result = append(result, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{Function: functionDef},
		})
	}
	return result
}

func fromOpenAIChatToolCalls(calls []openai.ChatCompletionMessageToolCallUnion) []ToolCall {
	result := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		if call.Type != "function" {
			continue
		}
		result = append(result, ToolCall{
			ID:   call.ID,
			Type: "function",
			Function: FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return result
}

// openAIStreamChat executes a streaming chat completion via the OpenAI SDK.
// It emits text and tool-call deltas through the adapter and accumulates the
// full response. Returns the accumulated ChatCompletion and any reasoning
// content extracted from streaming delta extra fields.
func openAIStreamChat(ctx context.Context, client openai.Client, params openai.ChatCompletionNewParams, adapter *streamAdapter, opts ...oaioption.RequestOption) (*openai.ChatCompletion, string, error) {
	stream := client.Chat.Completions.NewStreaming(ctx, params, opts...)

	var acc openai.ChatCompletionAccumulator
	var reasoning strings.Builder
	var toolCallSignaled bool
	// SDK's AddChunk sums Usage across chunks via +=, which is correct for
	// providers that emit usage only on the final chunk (OpenAI, Moonshot,
	// Zhipu). But SiliconFlow emits the full usage on every chunk, causing
	// N× inflation. Track the last non-zero usage and overwrite acc.Usage
	// before returning so both cases produce the correct final value.
	var lastUsage openai.CompletionUsage
	var sawUsage bool
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			lastUsage = chunk.Usage
			sawUsage = true
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			adapter.EmitText(delta.Content)
		}
		if len(delta.ToolCalls) > 0 && !toolCallSignaled {
			toolCallSignaled = true
			if name := delta.ToolCalls[0].Function.Name; name != "" {
				adapter.EmitToolCall(name)
			}
		}
		// Accumulate reasoning from non-standard extra fields.
		// Models return reasoning text in different delta fields:
		//   - "reasoning_content": DeepSeek, Moonshot, Zhipu, Minimax
		//   - "reasoning": Qwen via OpenRouter (and other Alibaba-routed models)
		for _, field := range []string{"reasoning_content", "reasoning"} {
			rc := delta.JSON.ExtraFields[field]
			raw := rc.Raw()
			if raw != "" && raw != "null" {
				var s string
				if json.Unmarshal([]byte(raw), &s) == nil && s != "" {
					reasoning.WriteString(s)
					break
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, "", err
	}

	if sawUsage {
		acc.Usage = lastUsage
	}

	return &acc.ChatCompletion, reasoning.String(), nil
}

// Chat sends a chat completion request to OpenRouter.
func (p *OpenRouterProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, SupportsVision("openrouter", p.modelType), SupportsAudio("openrouter", p.modelType), SupportsPDF("openrouter", p.modelType))
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	meta := openRouterModels[p.modelType]
	thinkingEnabled := len(meta.ThinkingOpts) > 0
	logger.Info(
		"openrouter request",
		"provider", "openrouter",
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
	if p.temperature != 0 {
		chatReq.Temperature = openai.Float(p.temperature)
	}

	requestOpts := []oaioption.RequestOption{}
	requestOpts = append(requestOpts, meta.ThinkingOpts...)
	if len(meta.ProviderOrder) > 0 {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("provider.order", meta.ProviderOrder),
		)
	}
	// Enable prompt caching for Anthropic models.
	// OpenRouter auto-places the cache breakpoint at the last cacheable block,
	// caching the full prefix (tools + system + conversation history).
	// Requires deterministic serialization — tools (tools.Defs), skills (skills.List),
	// and session summaries (buildSessionsSummary) must be sorted.
	if strings.HasPrefix(p.modelType, "anthropic/") {
		requestOpts = append(requestOpts,
			oaioption.WithJSONSet("cache_control", map[string]any{"type": "ephemeral"}),
		)
	}

	sessionKey := SessionKeyFromContext(ctx)
	toolsH, toolCount := hashChatToolParams(chatReq.Tools)
	prefixes := hashChatMessagePrefixes(chatReq.Messages)
	logger.Info(
		"openrouter prefix-hash",
		"provider", "openrouter",
		"sessionKey", sessionKey,
		"toolsH", toolsH,
		"toolCount", toolCount,
		"msgCount", len(chatReq.Messages),
		"prefixes", prefixes,
	)
	dumpFirstMessage("openrouter", sessionKey, chatReq.Messages)

	resp := &Response{ProviderLabel: "openrouter", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()

		chatResp, streamReasoning, err := openAIStreamChat(ctx, p.client, chatReq, adapter, requestOpts...)
		if err != nil {
			logger.Error("openrouter request send error", "provider", "openrouter", "err", err)
			adapter.SetError(fmt.Errorf("request failed: %w", err))
			return
		}

		if len(chatResp.Choices) == 0 {
			logger.Error("openrouter no choices", "provider", "openrouter")
			adapter.SetError(fmt.Errorf("no choices in response"))
			return
		}

		choice := chatResp.Choices[0]
		toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
		reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
		rawMessage := choice.Message.RawJSON()
		rawResponse := chatResp.RawJSON()
		reasoningText := extractReasoningText(rawMessage)
		if reasoningText == "" && streamReasoning != "" {
			reasoningText = streamReasoning
		}
		reasoningDetails := extractReasoningDetails(rawMessage)
		finalContent := choice.Message.Content
		finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "openrouter", toolCalls)

		cachedTokens := chatResp.Usage.PromptTokensDetails.CachedTokens
		logger.Info(
			"openrouter response",
			"provider", "openrouter",
			"modelType", p.modelType,
			"modelName", p.modelName,
			"finishReason", choice.FinishReason,
			"reasoningInResponse", reasoningTokens > 0,
			"hasToolCalls", len(toolCalls) > 0,
			"toolCallCount", len(toolCalls),
			"promptTokens", chatResp.Usage.PromptTokens,
			"completionTokens", chatResp.Usage.CompletionTokens,
			"reasoningTokens", reasoningTokens,
			"cachedTokens", cachedTokens,
			"totalTokens", chatResp.Usage.TotalTokens,
			"outputChars", len(choice.Message.Content),
			"latencyMs", time.Since(start).Milliseconds(),
		)
		logger.Debug(
			"openrouter raw output",
			"rawMessage", rawMessage,
			"rawResponse", rawResponse,
			"reasoningText", reasoningText,
		)

		resp.Content = finalContent
		resp.ReasoningContent = reasoningText
		resp.ReasoningDetails = reasoningDetails
		resp.ToolCalls = toolCalls
		resp.Usage = Usage{
			PromptTokens:     int(chatResp.Usage.PromptTokens),
			CompletionTokens: int(chatResp.Usage.CompletionTokens),
			TotalTokens:      int(chatResp.Usage.TotalTokens),
			CachedTokens:     int(cachedTokens),
			ReasoningTokens:  int(reasoningTokens),
		}
	}()

	return adapter.Result(), nil
}
