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
		ProviderOrder: []string{"qwen"},
	},
	"google/gemini-3-flash-preview": {
		ThinkingOpts: []oaioption.RequestOption{
			oaioption.WithJSONSet("reasoning", map[string]any{"effort": "high"}),
		},
		ProviderOrder: []string{"google-ai-studio"},
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
		Models:       []string{"moonshotai/kimi-k2.5", "anthropic/claude-sonnet-4.6", "anthropic/claude-opus-4.6", "z-ai/glm-5", "minimax/minimax-m2.5", "minimax/minimax-m2.7", "qwen/qwen3.5-35b-a3b", "google/gemini-3-flash-preview", "xiaomi/mimo-v2-pro", "xiaomi/mimo-v2-omni"},
		VisionModels: []string{"moonshotai/kimi-k2.5", "anthropic/claude-sonnet-4.6", "anthropic/claude-opus-4.6", "qwen/qwen3.5-35b-a3b", "google/gemini-3-flash-preview", "xiaomi/mimo-v2-pro", "xiaomi/mimo-v2-omni"},
		AudioModels:  []string{"google/gemini-3-flash-preview", "xiaomi/mimo-v2-omni"},
		ContextWindows: map[string]int{
			"moonshotai/kimi-k2.5":          262144,
			"anthropic/claude-sonnet-4.6":   1048576,
			"anthropic/claude-opus-4.6":     1048576,
			"z-ai/glm-5":                   200000,
			"minimax/minimax-m2.5":          196608,
			"minimax/minimax-m2.7":          204800,
			"qwen/qwen3.5-35b-a3b":         262144,
			"google/gemini-3-flash-preview": 1048576,
			"xiaomi/mimo-v2-pro":            1048576,
			"xiaomi/mimo-v2-omni":           262144,
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




func toOpenAIChatMessages(messages []Message, visionCapable, audioCapable bool) ([]openai.ChatCompletionMessageParamUnion, error) {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case "system":
			result = append(result, openai.SystemMessage(m.Content))
		case "user":
			result = append(result, openai.UserMessage(m.Content))
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
					if (isImage && !visionCapable) || (isAudio && !audioCapable) {
						continue
					}
					if !isImage && !isAudio {
						continue
					}
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					if isImage {
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
					result = append(result, openai.ChatCompletionMessageParamUnion{
						OfUser: &openai.ChatCompletionUserMessageParam{
							Content: openai.ChatCompletionUserMessageParamContentUnion{
								OfArrayOfContentParts: parts,
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
					assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tc.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
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

// Chat sends a chat completion request to OpenRouter.
func (p *OpenRouterProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, SupportsVision("openrouter", p.modelType), SupportsAudio("openrouter", p.modelType))
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


	chatResp, err := p.client.Chat.Completions.New(ctx, chatReq, requestOpts...)
	if err != nil {
		logger.Error("openrouter request send error", "provider", "openrouter", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		logger.Error("openrouter no choices", "provider", "openrouter")
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	toolCalls := fromOpenAIChatToolCalls(choice.Message.ToolCalls)
	reasoningTokens := chatResp.Usage.CompletionTokensDetails.ReasoningTokens
	rawMessage := choice.Message.RawJSON()
	rawResponse := chatResp.RawJSON()
	reasoningText := extractReasoningText(rawMessage)
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

	return &Response{
		Content:          finalContent,
		ReasoningContent: reasoningText,
		ReasoningDetails: reasoningDetails,
		ToolCalls:        toolCalls,
		Usage: Usage{
			PromptTokens:     int(chatResp.Usage.PromptTokens),
			CompletionTokens: int(chatResp.Usage.CompletionTokens),
			TotalTokens:      int(chatResp.Usage.TotalTokens),
			CachedTokens:     int(cachedTokens),
		},
		ProviderLabel: "openrouter",
		ModelLabel:    p.modelName,
	}, nil
}
