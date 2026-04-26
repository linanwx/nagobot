// Package provider defines the LLM provider interface and common types.
package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// Provider is the interface for LLM providers.
type Provider interface {
	// Chat sends a chat completion request and returns a ChatResult.
	// Use type assertion to check for StreamChatResult if streaming is needed.
	Chat(ctx context.Context, req *Request) (ChatResult, error)
}

// AccountIDSetter is optionally implemented by providers that need an account ID
// (e.g. OpenAI OAuth with ChatGPT-Account-ID header).
type AccountIDSetter interface {
	SetAccountID(id string)
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
	Media            []string   `json:"media,omitempty"`             // media markers like <<media:image/jpeg:/path>>
	ReasoningContent string          `json:"reasoning_content,omitempty"` // reasoning text for providers that require it
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"` // opaque reasoning details (Gemini thought_signature)
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`        // for assistant messages
	ToolCallID       string     `json:"tool_call_id,omitempty"`      // for tool result messages
	Name             string     `json:"name,omitempty"`              // tool name for tool results
	ID               string     `json:"id,omitempty"`                // unique message identifier
	Timestamp        time.Time  `json:"timestamp,omitempty"`         // when message was created
	Compressed       string     `json:"compressed,omitempty"`        // compressed version of content
	ReasoningTrimmed bool       `json:"reasoning_trimmed,omitempty"` // Tier 1 flag: reasoning marked for send-time exclusion (original data preserved)
	ReasoningTokens  int        `json:"reasoning_tokens,omitempty"`  // precise reasoning token count from provider API
	HeartbeatTrim    bool       `json:"heartbeat_trim,omitempty"`    // Tier 1 flag: heartbeat turn marked for send-time removal
	SkipTrim         bool       `json:"skip_trim,omitempty"`         // tool result must not be compressed (e.g. compression summary)
	Source           string     `json:"source,omitempty"`            // wake source that triggered this message
	OriginalContent  string     `json:"original_content,omitempty"`  // pre-rephrase content (set by rephrase agent)
}

// GetContent returns the compressed content if available, otherwise the original content.
func (m Message) GetContent() string {
	if m.Compressed != "" {
		return m.Compressed
	}
	return m.Content
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

// Quota holds rate-limit information extracted from API response headers.
type Quota struct {
	LimitRequests     int       `json:"limit_requests"`
	LimitTokens       int       `json:"limit_tokens"`
	RemainingRequests int       `json:"remaining_requests"`
	RemainingTokens   int       `json:"remaining_tokens"`
	ResetRequests     string    `json:"reset_requests,omitempty"`
	ResetTokens       string    `json:"reset_tokens,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Response represents a chat completion response.
type Response struct {
	Content          string          // final text response
	ReasoningContent string          // reasoning text (provider-specific)
	ReasoningDetails json.RawMessage // opaque reasoning details (Gemini thought_signature)
	ToolCalls        []ToolCall      // tool calls (if any)
	Usage            Usage           // token usage
	Quota            *Quota          // rate-limit quota (optional, provider-specific)
	ProviderLabel    string          // effective provider name for metrics (e.g. "openai" vs "openai-oauth")
	ModelLabel       string          // effective model name for metrics
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
	CachedTokens     int `json:"cached_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
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
	Models         []string
	VisionModels   []string       // Subset of Models that support image input.
	AudioModels    []string       // Subset of Models that support audio input.
	PDFModels      []string       // Subset of Models that support PDF document input.
	ContextWindows map[string]int // model key -> context window size in tokens
	EnvKey         string
	EnvBase        string
	Constructor    ProviderConstructor
}

// supportedModelTypes is the whitelist of supported model types.
var supportedModelTypes = map[string]bool{}

// providerModelTypes maps providers to their supported model types.
var providerModelTypes = map[string][]string{}

// visionCapable tracks provider:model pairs that support image input.
var visionCapable = map[string]bool{}

// audioCapable tracks provider:model pairs that support audio input.
var audioCapable = map[string]bool{}

// pdfCapable tracks provider:model pairs that support PDF document input.
var pdfCapable = map[string]bool{}

// providerModelContextWindows maps "provider:model" to context window size in tokens.
// Keyed per provider so that a model accessed via different routes (e.g. openai vs
// openai-oauth) can have different effective limits — OAuth via the ChatGPT codex
// backend is account-plan-throttled (~272K for Plus) even when the underlying model
// supports a 1M window via direct API key.
var providerModelContextWindows = map[string]int{}

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
	for _, vm := range reg.VisionModels {
		vm = strings.TrimSpace(vm)
		if vm != "" {
			visionCapable[name+":"+vm] = true
		}
	}
	for _, am := range reg.AudioModels {
		am = strings.TrimSpace(am)
		if am != "" {
			audioCapable[name+":"+am] = true
		}
	}
	for _, pm := range reg.PDFModels {
		pm = strings.TrimSpace(pm)
		if pm != "" {
			pdfCapable[name+":"+pm] = true
		}
	}
	for k, v := range reg.ContextWindows {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		providerModelContextWindows[name+":"+k] = v
	}
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

// SupportsVision reports whether a provider+model combination supports image input.
func SupportsVision(providerName, modelType string) bool {
	return visionCapable[providerName+":"+modelType]
}

// SupportsAudio reports whether a provider+model combination supports audio input.
func SupportsAudio(providerName, modelType string) bool {
	return audioCapable[providerName+":"+modelType]
}

// SupportsPDF reports whether a provider+model combination supports PDF document input.
func SupportsPDF(providerName, modelType string) bool {
	return pdfCapable[providerName+":"+modelType]
}

// ContextWindowForModel returns the context window size in tokens for a
// provider+model pair. Returns 0 if unknown.
func ContextWindowForModel(providerName, modelType string) int {
	return providerModelContextWindows[providerName+":"+modelType]
}

// IsSupportedModel returns true if the model type is registered in any provider.
func IsSupportedModel(modelType string) bool {
	return supportedModelTypes[modelType]
}

// ProviderForModel returns the first provider that supports the given model type.
// Returns empty string if no provider is found.
func ProviderForModel(modelType string) string {
	for provName, models := range providerModelTypes {
		for _, m := range models {
			if m == modelType {
				return provName
			}
		}
	}
	return ""
}

// EffectiveContextWindow returns min(modelContextWindow, configuredWindow).
// If the model context window is unknown (0), returns configuredWindow.
func EffectiveContextWindow(providerName, modelType string, configuredWindow int) int {
	modelWindow := ContextWindowForModel(providerName, modelType)
	if modelWindow <= 0 {
		return configuredWindow
	}
	if configuredWindow <= 0 {
		return modelWindow
	}
	if modelWindow < configuredWindow {
		return modelWindow
	}
	return configuredWindow
}

// IsKimiModel returns true if the model type is a Kimi model.
func IsKimiModel(modelType string) bool {
	return strings.Contains(modelType, "kimi")
}

// UserMessage creates a user message.
func UserMessage(content string) Message {
	return Message{Role: "user", Content: content, Timestamp: time.Now()}
}

// SystemMessage creates a system message.
func SystemMessage(content string) Message {
	return Message{Role: "system", Content: content, Timestamp: time.Now()}
}

// AssistantMessage creates an assistant message.
func AssistantMessage(content string) Message {
	return Message{Role: "assistant", Content: content, Timestamp: time.Now()}
}

// AssistantMessageWithTools creates an assistant message with tool calls.
func AssistantMessageWithTools(content, reasoningContent string, reasoningDetails json.RawMessage, toolCalls []ToolCall) Message {
	return Message{Role: "assistant", Content: content, ReasoningContent: reasoningContent, ReasoningDetails: reasoningDetails, ToolCalls: toolCalls, Timestamp: time.Now()}
}

// ToolResultMessage creates a tool result message.
func ToolResultMessage(toolCallID, name, content string) Message {
	return Message{Role: "tool", ToolCallID: toolCallID, Name: name, Content: content, Timestamp: time.Now()}
}

// SanitizeMessages cleans up message sequences to prevent API errors:
//  1. Strips tool_calls whose tool responses don't immediately follow.
//  2. Removes orphaned tool messages (no preceding assistant with matching tool_call ID).
//  3. Drops empty assistant messages (no content, no reasoning, no tool calls).
func SanitizeMessages(messages []Message) []Message {
	// For each assistant with tool_calls, check that the immediately following
	// messages are the corresponding tool responses (no gaps allowed).
	answeredCalls := make(map[string]bool)
	for i, m := range messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		tcIDs := make(map[string]bool, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			tcIDs[tc.ID] = true
		}
		// Scan immediately following tool messages.
		for j := i + 1; j < len(messages); j++ {
			if messages[j].Role != "tool" {
				break
			}
			if tcIDs[messages[j].ToolCallID] {
				answeredCalls[messages[j].ToolCallID] = true
			}
		}
	}

	// Forward scan: track tool_call IDs from assistant messages.
	callIDs := make(map[string]bool)
	result := make([]Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// Keep only tool_calls that have immediately following tool responses.
			var answered []ToolCall
			for _, tc := range m.ToolCalls {
				if answeredCalls[tc.ID] {
					answered = append(answered, tc)
					callIDs[tc.ID] = true
				}
			}
			m.ToolCalls = answered
			if len(m.ToolCalls) == 0 {
				m.ReasoningDetails = nil
			}
		}
		// Drop messages with empty role.
		if m.Role == "" {
			continue
		}
		// Drop assistant messages with no visible content and no tool calls.
		// This covers both completely empty messages and messages with only
		// reasoning (from interrupted/incomplete turns where tool_calls were
		// stripped as orphaned). Dropping avoids "(empty)" backfill artifacts.
		if m.Role == "assistant" && m.Content == "" && m.Compressed == "" && len(m.ToolCalls) == 0 {
			continue
		}
		if m.Role == "tool" && !callIDs[m.ToolCallID] {
			continue
		}
		result = append(result, m)
	}
	return result
}

// StripReasoningKeepSignatures removes reasoning content from ReasoningDetails
// while preserving function-call signatures required by Gemini API round-trip.
// For non-Gemini formats (Anthropic, OpenAI, OpenRouter) this returns nil.
func StripReasoningKeepSignatures(details json.RawMessage) json.RawMessage {
	if len(details) == 0 {
		return nil
	}
	// Probe for Gemini-style parts: look for functionCall + thoughtSignature.
	type probeFC struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args,omitempty"`
	}
	type probePart struct {
		FunctionCall     *probeFC `json:"functionCall,omitempty"`
		ThoughtSignature string   `json:"thoughtSignature,omitempty"`
	}
	var parts []probePart
	if err := json.Unmarshal(details, &parts); err != nil {
		return nil
	}
	// Keep raw JSON of parts that have both functionCall and thoughtSignature.
	var rawParts []json.RawMessage
	if err := json.Unmarshal(details, &rawParts); err != nil {
		return nil
	}
	var kept []json.RawMessage
	for i, p := range parts {
		if p.FunctionCall != nil && p.ThoughtSignature != "" {
			kept = append(kept, rawParts[i])
		}
	}
	if len(kept) == 0 {
		return nil
	}
	result, err := json.Marshal(kept)
	if err != nil {
		return nil
	}
	return result
}

// MediaMarker represents an embedded media reference in tool output.
type MediaMarker struct {
	MimeType string
	FilePath string
}

var mediaMarkerRe = regexp.MustCompile(`<<media:([^:>]+):([^>]+)>>`)

// ParseMediaMarkers extracts <<media:mime:path>> markers from text,
// returning the cleaned text (markers removed) and the parsed markers.
func ParseMediaMarkers(text string) (string, []MediaMarker) {
	matches := mediaMarkerRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}
	var markers []MediaMarker
	cleaned := text
	// Process in reverse order to keep indices valid.
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		mime := text[m[2]:m[3]]
		path := text[m[4]:m[5]]
		markers = append([]MediaMarker{{MimeType: mime, FilePath: path}}, markers...)
		cleaned = cleaned[:m[0]] + cleaned[m[1]:]
	}
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, markers
}

// inputChars estimates the total character count of a message slice
// for logging purposes. Used by all providers.
func inputChars(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Role)
		total += len(m.Content)
	}
	return total
}

// resolveContentWithReasoningFallback returns reasoningText as content
// when finalContent is empty and there are no tool calls.
// This handles LLMs that put useful output in reasoning but leave content empty.
func resolveContentWithReasoningFallback(finalContent, reasoningText, providerName string, toolCalls []ToolCall) string {
	if strings.TrimSpace(finalContent) == "" && len(toolCalls) == 0 && strings.TrimSpace(reasoningText) != "" {
		logger.Warn(providerName+" response content empty, using reasoning text fallback")
		return reasoningText
	}
	return finalContent
}

// ReadFileAsBase64 reads a file and returns its contents as a base64-encoded string.
func ReadFileAsBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
