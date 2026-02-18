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

	return ""
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
}

func init() {
	RegisterProvider("openrouter", ProviderRegistration{
		Models:       []string{"moonshotai/kimi-k2.5"},
		VisionModels: []string{"moonshotai/kimi-k2.5"},
		EnvKey:       "OPENROUTER_API_KEY",
		EnvBase:      "OPENROUTER_API_BASE",
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

func openRouterInputChars(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Role)
		total += len(m.Content)
	}
	return total
}

func toOpenAIChatMessages(messages []Message, visionCapable bool) ([]openai.ChatCompletionMessageParamUnion, error) {
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
			// Chat Completions doesn't support images in tool messages.
			// Inject a synthetic user message with the image as a workaround.
			if visionCapable && len(markers) > 0 {
				var parts []openai.ChatCompletionContentPartUnionParam
				for _, marker := range markers {
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					parts = append(parts, openai.ChatCompletionContentPartUnionParam{
						OfImageURL: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL: "data:" + marker.MimeType + ";base64," + b64,
							},
						},
					})
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
			if m.Content != "" {
				assistant.Content.OfString = openai.String(m.Content)
			} else if len(m.ToolCalls) > 0 {
				// Some upstream providers (e.g. Moonshot via OpenRouter) reject empty
				// assistant messages even when tool_calls are present.
				assistant.Content.OfString = openai.String("(tool call)")
			} else {
				assistant.Content.OfString = openai.String("(empty assistant message)")
			}
			if len(m.ToolCalls) > 0 {
				reasoningContent := strings.TrimSpace(m.ReasoningContent)
				if reasoningContent == "" {
					reasoningContent = strings.TrimSpace(m.Content)
				}
				if reasoningContent == "" {
					reasoningContent = "(tool call)"
				}
				assistant.SetExtraFields(map[string]any{
					"reasoning_content": reasoningContent,
				})
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
	inputChars := openRouterInputChars(req.Messages)

	messages, err := toOpenAIChatMessages(req.Messages, SupportsVision("openrouter", p.modelType))
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
	finalContent := choice.Message.Content
	if strings.TrimSpace(finalContent) == "" && len(toolCalls) == 0 && strings.TrimSpace(reasoningText) != "" {
		logger.Warn("openrouter response content empty, using reasoning text fallback")
		finalContent = reasoningText
	}

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
		ToolCalls:        toolCalls,
		Usage: Usage{
			PromptTokens:     int(chatResp.Usage.PromptTokens),
			CompletionTokens: int(chatResp.Usage.CompletionTokens),
			TotalTokens:      int(chatResp.Usage.TotalTokens),
		},
	}, nil
}
