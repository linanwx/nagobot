// Package provider provides LLM provider implementations.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	aoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/linanwx/nagobot/logger"
)

const (
	anthropicAPIBase = "https://api.anthropic.com"
)

func init() {
	RegisterProvider("anthropic", ProviderRegistration{
		Models:  []string{"claude-sonnet-4-5", "claude-opus-4-6"},
		EnvKey:  "ANTHROPIC_API_KEY",
		EnvBase: "ANTHROPIC_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return NewAnthropicProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
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
	case "claude-sonnet-4-5", "claude-opus-4-6":
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

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *AnthropicProvider {
	if modelName == "" {
		modelName = modelType
	}

	baseURL := normalizeSDKBaseURL(apiBase, anthropicAPIBase, "/v1/messages")
	client := anthropic.NewClient(
		aoption.WithAPIKey(apiKey),
		aoption.WithBaseURL(baseURL),
		aoption.WithMaxRetries(sdkMaxRetries),
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

func parseFunctionArguments(arguments string) any {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}
	}

	var parsed any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return arguments
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
			msgList = append(msgList, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			flushPendingToolResults()

			blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
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
			pendingToolResults = append(pendingToolResults, anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
				ToolUseID: m.ToolCallID,
				Content: []anthropic.ToolResultBlockParamContentUnion{{
					OfText: &anthropic.TextBlockParam{Text: m.Content},
				}},
			}})
		default:
			return "", nil, fmt.Errorf("unsupported message role: %s", m.Role)
		}
	}

	flushPendingToolResults()
	return systemPrompt, msgList, nil
}

// Chat sends a chat completion request to Anthropic.
func (p *AnthropicProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
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
		params.System = []anthropic.TextBlockParam{{Text: systemPrompt}}
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
		logger.Warn(
			"anthropic temperature adjusted for thinking constraints",
			"provider", "anthropic",
			"modelType", p.modelType,
			"configuredTemperature", p.temperature,
			"requestTemperature", requestTemp,
		)
	}

	messageResp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		logger.Error("anthropic request send error", "provider", "anthropic", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	var textParts []string
	var reasoningParts []string
	toolCalls := make([]ToolCall, 0)
	for _, block := range messageResp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				reasoningParts = append(reasoningParts, strings.TrimSpace(block.Thinking))
			}
		case "redacted_thinking":
			reasoningParts = append(reasoningParts, "[redacted_thinking]")
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
			})
		}
	}

	content := strings.Join(textParts, "\n")
	reasoningContent := strings.Join(reasoningParts, "\n")
	logger.Info(
		"anthropic response",
		"provider", "anthropic",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", messageResp.StopReason,
		"reasoningInResponse", strings.TrimSpace(reasoningContent) != "",
		"hasToolCalls", len(toolCalls) > 0,
		"toolCallCount", len(toolCalls),
		"promptTokens", messageResp.Usage.InputTokens,
		"completionTokens", messageResp.Usage.OutputTokens,
		"totalTokens", messageResp.Usage.InputTokens+messageResp.Usage.OutputTokens,
		"outputChars", len(content),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return &Response{
		Content:          content,
		ReasoningContent: reasoningContent,
		ToolCalls:        toolCalls,
		Usage: Usage{
			PromptTokens:     int(messageResp.Usage.InputTokens),
			CompletionTokens: int(messageResp.Usage.OutputTokens),
			TotalTokens:      int(messageResp.Usage.InputTokens + messageResp.Usage.OutputTokens),
		},
	}, nil
}
