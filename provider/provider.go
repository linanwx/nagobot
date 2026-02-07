// Package provider defines the LLM provider interface and common types.
package provider

import (
	"context"
	"errors"
	"sort"
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
	Role             string     `json:"role"`                        // system, user, assistant, tool
	Content          string     `json:"content,omitempty"`           // text content
	ReasoningContent string     `json:"reasoning_content,omitempty"` // reasoning text for providers that require it
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`        // for assistant messages
	ToolCallID       string     `json:"tool_call_id,omitempty"`      // for tool result messages
	Name             string     `json:"name,omitempty"`              // tool name for tool results
}

// ToolCall represents a tool invocation by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call within a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Response represents a chat completion response.
type Response struct {
	Content          string     // final text response
	ReasoningContent string     // reasoning text (provider-specific)
	ToolCalls        []ToolCall // tool calls (if any)
	Usage            Usage      // token usage
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

// ProviderConstructor builds a provider for the requested model/runtime settings.
type ProviderConstructor func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider

// ProviderRegistration defines metadata and constructor for a provider.
type ProviderRegistration struct {
	Models      []string
	EnvKey      string
	EnvBase     string
	Constructor ProviderConstructor
}

// supportedModelTypes is the whitelist of supported model types.
var supportedModelTypes = map[string]bool{}

// providerModelTypes maps providers to their supported model types.
var providerModelTypes = map[string][]string{}

var providerRegistry = map[string]ProviderRegistration{}

// RegisterProvider registers provider metadata and constructor.
func RegisterProvider(name string, reg ProviderRegistration) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}

	models := make([]string, 0, len(reg.Models))
	for _, model := range reg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		models = append(models, model)
		supportedModelTypes[model] = true
	}

	reg.Models = models
	reg.EnvKey = strings.TrimSpace(reg.EnvKey)
	reg.EnvBase = strings.TrimSpace(reg.EnvBase)
	providerRegistry[name] = reg
	providerModelTypes[name] = append([]string(nil), models...)
}

// SupportedProviders returns all supported provider names in sorted order.
func SupportedProviders() []string {
	names := make([]string, 0, len(providerModelTypes))
	for name := range providerModelTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SupportedModelsForProvider returns supported model types for the given provider.
func SupportedModelsForProvider(providerName string) []string {
	models, ok := providerModelTypes[providerName]
	if !ok {
		return nil
	}
	out := make([]string, len(models))
	copy(out, models)
	return out
}

// ValidateProviderModelType checks if a model type is valid for a provider.
func ValidateProviderModelType(providerName, modelType string) error {
	if !supportedModelTypes[modelType] {
		return errors.New("unsupported model type: " + modelType)
	}

	allowed, ok := providerModelTypes[providerName]
	if !ok {
		return errors.New("unknown provider: " + providerName)
	}

	for _, m := range allowed {
		if m == modelType {
			return nil
		}
	}

	return errors.New("model type " + modelType + " is not supported by provider " + providerName)
}

// IsKimiModel returns true if the model type is a Kimi model.
func IsKimiModel(modelType string) bool {
	return strings.Contains(modelType, "kimi")
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
func AssistantMessageWithTools(content, reasoningContent string, toolCalls []ToolCall) Message {
	return Message{Role: "assistant", Content: content, ReasoningContent: reasoningContent, ToolCalls: toolCalls}
}

// ToolResultMessage creates a tool result message.
func ToolResultMessage(toolCallID, name, content string) Message {
	return Message{Role: "tool", ToolCallID: toolCallID, Name: name, Content: content}
}
