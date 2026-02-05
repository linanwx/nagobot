// Package provider defines the LLM provider interface and common types.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Provider is the interface for LLM providers.
type Provider interface {
	// Chat sends a chat completion request and returns the response.
	Chat(ctx context.Context, req *Request) (*Response, error)
}

// Request represents a chat completion request.
type Request struct {
	Messages []Message
	Tools    []ToolDef
}

// Message represents a chat message in OpenAI format (internal canonical format).
type Message struct {
	Role       string     `json:"role"`                   // system, user, assistant, tool
	Content    string     `json:"content,omitempty"`      // text content
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // for assistant messages
	ToolCallID string     `json:"tool_call_id,omitempty"` // for tool result messages
	Name       string     `json:"name,omitempty"`         // tool name for tool results
}

// ToolCall represents a tool invocation by the model.
type ToolCall struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"` // "function"
	Function  FunctionCall    `json:"function"`
	Arguments json.RawMessage `json:"-"` // parsed from Function.Arguments
}

// FunctionCall represents a function call within a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Response represents a chat completion response.
type Response struct {
	Content   string     // final text response
	ToolCalls []ToolCall // tool calls (if any)
	Usage     Usage      // token usage
}

// HasToolCalls returns true if the response contains tool calls.
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// Usage represents token usage information.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ToolDef defines a tool for the LLM (OpenAI function calling format).
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef defines a function that the model can call.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ModelTypeInfo contains information about a supported model type.
type ModelTypeInfo struct {
	HasThinking bool // whether the model supports thinking mode
}

// supportedModelTypes is the whitelist of supported model types.
var supportedModelTypes = map[string]ModelTypeInfo{
	"moonshotai/kimi-k2.5": {HasThinking: true},
	"claude-sonnet-4-5":    {HasThinking: true},
	"claude-opus-4-5":      {HasThinking: true},
}

// providerModelTypes maps providers to their supported model types.
var providerModelTypes = map[string][]string{
	"openrouter": {"moonshotai/kimi-k2.5"},
	"anthropic":  {"claude-sonnet-4-5", "claude-opus-4-5"},
}

// ValidateModelType checks if a model type is supported.
func ValidateModelType(modelType string) error {
	if _, ok := supportedModelTypes[modelType]; !ok {
		return errors.New("unsupported model type: " + modelType)
	}
	return nil
}

// ValidateProviderModelType checks if a model type is valid for a provider.
func ValidateProviderModelType(provider, modelType string) error {
	if err := ValidateModelType(modelType); err != nil {
		return err
	}

	allowed, ok := providerModelTypes[provider]
	if !ok {
		return errors.New("unknown provider: " + provider)
	}

	for _, m := range allowed {
		if m == modelType {
			return nil
		}
	}

	return errors.New("model type " + modelType + " is not supported by provider " + provider)
}

// GetModelTypeInfo returns info about a model type.
func GetModelTypeInfo(modelType string) (ModelTypeInfo, bool) {
	info, ok := supportedModelTypes[modelType]
	return info, ok
}

// IsKimiModel returns true if the model type is a Kimi model.
func IsKimiModel(modelType string) bool {
	return strings.Contains(modelType, "kimi")
}

// IsClaudeModel returns true if the model type is a Claude model.
func IsClaudeModel(modelType string) bool {
	return strings.Contains(modelType, "claude")
}

// UserMessage creates a user message.
func UserMessage(content string) Message {
	return Message{Role: "user", Content: content}
}

// SystemMessage creates a system message.
func SystemMessage(content string) Message {
	return Message{Role: "system", Content: content}
}

// AssistantMessage creates an assistant message.
func AssistantMessage(content string) Message {
	return Message{Role: "assistant", Content: content}
}

// AssistantMessageWithTools creates an assistant message with tool calls.
func AssistantMessageWithTools(content string, toolCalls []ToolCall) Message {
	return Message{Role: "assistant", Content: content, ToolCalls: toolCalls}
}

// ToolResultMessage creates a tool result message.
func ToolResultMessage(toolCallID, name, content string) Message {
	return Message{Role: "tool", ToolCallID: toolCallID, Name: name, Content: content}
}
