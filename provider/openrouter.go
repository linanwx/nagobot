// Package provider provides LLM provider implementations.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const (
	openRouterBaseURL = "https://openrouter.ai/api/v1/chat/completions"
)

// OpenRouterProvider implements the Provider interface for OpenRouter.
type OpenRouterProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
}

// NewOpenRouterProvider creates a new OpenRouter provider.
func NewOpenRouterProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *OpenRouterProvider {
	if modelName == "" {
		modelName = modelType
	}
	return &OpenRouterProvider{
		apiKey:      apiKey,
		apiBase:     apiBase,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{},
	}
}

// openRouterRequest is the request body for OpenRouter API.
type openRouterRequest struct {
	Model       string          `json:"model"`
	Messages    []openRouterMsg `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Tools       []ToolDef       `json:"tools,omitempty"`
	ExtraBody   *kimiExtraBody  `json:"extra_body,omitempty"`
}

// openRouterMsg is a message in OpenRouter format.
type openRouterMsg struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openRouterTool `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

// openRouterTool represents a tool call in OpenRouter format.
type openRouterTool struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openRouterToolFunction `json:"function"`
}

// openRouterToolFunction represents a function call.
type openRouterToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// kimiExtraBody contains Kimi-specific parameters.
type kimiExtraBody struct {
	ChatTemplateKwargs *kimiChatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
}

// kimiChatTemplateKwargs contains Kimi chat template parameters.
type kimiChatTemplateKwargs struct {
	Thinking bool `json:"thinking"`
}

// openRouterResponse is the response from OpenRouter API.
type openRouterResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning,omitempty"`
			ReasoningDetails []struct {
				Type   string `json:"type,omitempty"`
				Text   string `json:"text,omitempty"`
				Format string `json:"format,omitempty"`
				Index  int    `json:"index,omitempty"`
			} `json:"reasoning_details,omitempty"`
			ToolCalls []openRouterTool `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens            int `json:"prompt_tokens"`
		CompletionTokens        int `json:"completion_tokens"`
		TotalTokens             int `json:"total_tokens"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details,omitempty"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func openRouterInputChars(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Role)
		total += len(m.Content)
	}
	return total
}

// Chat sends a chat completion request to OpenRouter.
func (p *OpenRouterProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := openRouterInputChars(req.Messages)

	// Convert messages to OpenRouter format
	msgs := make([]openRouterMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := openRouterMsg{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}

		// Convert tool calls if present
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]openRouterTool, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				msg.ToolCalls[i] = openRouterTool{
					ID:   tc.ID,
					Type: "function",
					Function: openRouterToolFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}

		msgs = append(msgs, msg)
	}

	// Build request body
	reqBody := openRouterRequest{
		Model:       p.modelName,
		Messages:    msgs,
		MaxTokens:   p.maxTokens,
		Temperature: p.temperature,
		Tools:       req.Tools,
	}

	// Add Kimi-specific thinking mode if this is a Kimi model
	thinkingEnabled := false
	if IsKimiModel(p.modelType) {
		thinkingEnabled = true
		reqBody.ExtraBody = &kimiExtraBody{
			ChatTemplateKwargs: &kimiChatTemplateKwargs{
				Thinking: true,
			},
		}
	}
	logger.Info(
		"openrouter request",
		"provider", "openrouter",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"thinkingEnabled", thinkingEnabled,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	// Marshal request
	body, err := json.Marshal(reqBody)
	if err != nil {
		logger.Error("openrouter request marshal error", "provider", "openrouter", "err", err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	baseURL := p.apiBase
	if baseURL == "" {
		baseURL = openRouterBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(body))
	if err != nil {
		logger.Error("openrouter request create error", "provider", "openrouter", "err", err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/linanwx/nagobot")
	httpReq.Header.Set("X-Title", "nagobot")

	// Send request
	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("openrouter request send error", "provider", "openrouter", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		logger.Error("openrouter response read error", "provider", "openrouter", "err", err)
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response
	var orResp openRouterResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		logger.Error("openrouter response parse error", "provider", "openrouter", "err", err)
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for API error
	if orResp.Error != nil {
		logger.Error("openrouter api error", "provider", "openrouter", "err", orResp.Error.Message)
		return nil, fmt.Errorf("API error: %s", orResp.Error.Message)
	}

	// Check for valid response
	if len(orResp.Choices) == 0 {
		logger.Error("openrouter no choices", "provider", "openrouter")
		return nil, fmt.Errorf("no choices in response")
	}

	choice := orResp.Choices[0]
	reasoningInResponse := choice.Message.Reasoning != "" || len(choice.Message.ReasoningDetails) > 0
	reasoningTokens := orResp.Usage.CompletionTokensDetails.ReasoningTokens

	// Convert tool calls
	var toolCalls []ToolCall
	if len(choice.Message.ToolCalls) > 0 {
		toolCalls = make([]ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			toolCalls[i] = ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
				Arguments: json.RawMessage(tc.Function.Arguments),
			}
		}
	}

	logger.Info(
		"openrouter response",
		"provider", "openrouter",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", choice.FinishReason,
		"reasoningInResponse", reasoningInResponse,
		"hasToolCalls", len(toolCalls) > 0,
		"toolCallCount", len(toolCalls),
		"promptTokens", orResp.Usage.PromptTokens,
		"completionTokens", orResp.Usage.CompletionTokens,
		"reasoningTokens", reasoningTokens,
		"totalTokens", orResp.Usage.TotalTokens,
		"outputChars", len(choice.Message.Content),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return &Response{
		Content:   choice.Message.Content,
		ToolCalls: toolCalls,
		Usage: Usage{
			PromptTokens:     orResp.Usage.PromptTokens,
			CompletionTokens: orResp.Usage.CompletionTokens,
			TotalTokens:      orResp.Usage.TotalTokens,
		},
	}, nil
}
