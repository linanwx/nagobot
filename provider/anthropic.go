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
	anthropicBaseURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
)

// AnthropicProvider implements the Provider interface for Anthropic.
type AnthropicProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *AnthropicProvider {
	if modelName == "" {
		modelName = modelType
	}
	return &AnthropicProvider{
		apiKey:      apiKey,
		apiBase:     apiBase,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{},
	}
}

// anthropicRequest is the request body for Anthropic API.
type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

// anthropicMessage is a message in Anthropic format.
type anthropicMessage struct {
	Role    string                  `json:"role"` // "user" or "assistant"
	Content []anthropicContentBlock `json:"content"`
}

// anthropicContentBlock represents a content block.
type anthropicContentBlock struct {
	Type      string          `json:"type"` // "text", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // for tool_use
	Name      string          `json:"name,omitempty"`        // for tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // for tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // for tool_result
	Content   string          `json:"content,omitempty"`     // for tool_result (string content)
	IsError   bool            `json:"is_error,omitempty"`    // for tool_result
}

// anthropicTool represents a tool definition for Anthropic.
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// anthropicResponse is the response from Anthropic API.
type anthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []anthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence string                  `json:"stop_sequence,omitempty"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func anthropicInputChars(systemPrompt string, messages []Message) int {
	total := len(systemPrompt)
	for _, m := range messages {
		switch m.Role {
		case "system":
			// already counted
		case "user", "assistant", "tool":
			total += len(m.Content)
		default:
			total += len(m.Content)
		}
	}
	return total
}

// Chat sends a chat completion request to Anthropic.
func (p *AnthropicProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()

	// Extract system message and convert messages to Anthropic format
	var systemPrompt string
	msgs := make([]anthropicMessage, 0)

	// Group consecutive tool results into a single user message
	var pendingToolResults []anthropicContentBlock

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			systemPrompt = m.Content

		case "user":
			// If we have pending tool results, add them first
			if len(pendingToolResults) > 0 {
				msgs = append(msgs, anthropicMessage{
					Role:    "user",
					Content: pendingToolResults,
				})
				pendingToolResults = nil
			}
			msgs = append(msgs, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{
					{Type: "text", Text: m.Content},
				},
			})

		case "assistant":
			// If we have pending tool results, add them first
			if len(pendingToolResults) > 0 {
				msgs = append(msgs, anthropicMessage{
					Role:    "user",
					Content: pendingToolResults,
				})
				pendingToolResults = nil
			}

			content := make([]anthropicContentBlock, 0)
			if m.Content != "" {
				content = append(content, anthropicContentBlock{
					Type: "text",
					Text: m.Content,
				})
			}
			// Convert tool calls to tool_use blocks
			for _, tc := range m.ToolCalls {
				content = append(content, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
				})
			}
			if len(content) > 0 {
				msgs = append(msgs, anthropicMessage{
					Role:    "assistant",
					Content: content,
				})
			}

		case "tool":
			// Accumulate tool results
			pendingToolResults = append(pendingToolResults, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
		}
	}

	inputChars := anthropicInputChars(systemPrompt, req.Messages)

	// Add any remaining tool results
	if len(pendingToolResults) > 0 {
		msgs = append(msgs, anthropicMessage{
			Role:    "user",
			Content: pendingToolResults,
		})
	}

	// Convert tools to Anthropic format
	var tools []anthropicTool
	for _, t := range req.Tools {
		tools = append(tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	logger.Info(
		"anthropic request",
		"provider", "anthropic",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"thinkingEnabled", false,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	// Build request body
	reqBody := anthropicRequest{
		Model:       p.modelName,
		MaxTokens:   p.maxTokens,
		Temperature: p.temperature,
		System:      systemPrompt,
		Messages:    msgs,
		Tools:       tools,
	}

	// Marshal request
	body, err := json.Marshal(reqBody)
	if err != nil {
		logger.Error("anthropic request marshal error", "provider", "anthropic", "err", err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	baseURL := p.apiBase
	if baseURL == "" {
		baseURL = anthropicBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(body))
	if err != nil {
		logger.Error("anthropic request create error", "provider", "anthropic", "err", err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	// Send request
	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("anthropic request send error", "provider", "anthropic", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		logger.Error("anthropic response read error", "provider", "anthropic", "err", err)
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response
	var antResp anthropicResponse
	if err := json.Unmarshal(respBody, &antResp); err != nil {
		logger.Error("anthropic response parse error", "provider", "anthropic", "err", err)
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for API error
	if antResp.Error != nil {
		logger.Error("anthropic api error", "provider", "anthropic", "err", antResp.Error.Message)
		return nil, fmt.Errorf("API error: %s", antResp.Error.Message)
	}

	// Extract content and tool calls
	var content string
	var toolCalls []ToolCall

	for _, block := range antResp.Content {
		switch block.Type {
		case "text":
			content = block.Text
		case "tool_use":
			tc := ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
				Arguments: block.Input,
			}
			toolCalls = append(toolCalls, tc)
		}
	}

	logger.Info(
		"anthropic response",
		"provider", "anthropic",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", antResp.StopReason,
		"reasoningInResponse", false,
		"hasToolCalls", len(toolCalls) > 0,
		"toolCallCount", len(toolCalls),
		"promptTokens", antResp.Usage.InputTokens,
		"completionTokens", antResp.Usage.OutputTokens,
		"totalTokens", antResp.Usage.InputTokens+antResp.Usage.OutputTokens,
		"outputChars", len(content),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return &Response{
		Content:   content,
		ToolCalls: toolCalls,
		Usage: Usage{
			PromptTokens:     antResp.Usage.InputTokens,
			CompletionTokens: antResp.Usage.OutputTokens,
			TotalTokens:      antResp.Usage.InputTokens + antResp.Usage.OutputTokens,
		},
	}, nil
}
