package opencodego

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type chatWireRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Tools         []chatTool    `json:"tools,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Effort      string   `json:"reasoning_effort,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}
type chatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	Reasoning  *string    `json:"reasoning_content,omitempty"`
	ToolCalls  []chatCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type chatCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}
type chatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}
type chatToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

func chatRequest(req Request) (chatWireRequest, error) {
	result := chatWireRequest{Model: req.Model, Stream: true, MaxTokens: req.MaxTokens, Effort: req.Effort}
	result.StreamOptions.IncludeUsage = true
	if req.Model == "step-5-preview-free" && (result.MaxTokens <= 0 || result.MaxTokens > 65536) {
		// The free Go route has a lower output limit than the base catalog model.
		result.MaxTokens = 65536
	}
	if strings.HasPrefix(req.Model, "glm-") {
		temperature := 1.0
		result.Temperature = &temperature
	}
	var pendingImages []Image
	flushImages := func() {
		if len(pendingImages) > 0 {
			result.Messages = append(result.Messages, chatMessage{Role: "user", Content: chatImageParts("", pendingImages)})
			pendingImages = nil
		}
	}
	for _, message := range req.Messages {
		switch message.Role {
		case "system", "user", "assistant":
			flushImages()
		case "tool":
			if message.ToolCallID == "" {
				return result, fmt.Errorf("OpenCode Go tool result has no tool call ID")
			}
		default:
			return result, fmt.Errorf("unsupported OpenCode Go chat role %q", message.Role)
		}
		wire := chatMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
		if message.Role == "assistant" && (strings.HasPrefix(req.Model, "deepseek-") || message.ReasoningContent != "") {
			reasoning := message.ReasoningContent
			wire.Reasoning = &reasoning
		}
		if len(message.Images) > 0 {
			if message.Role == "tool" {
				pendingImages = append(pendingImages, message.Images...)
			} else {
				wire.Content = chatImageParts(message.Content, message.Images)
			}
		}
		for _, tool := range message.ToolCalls {
			if message.Role != "assistant" || tool.ID == "" || tool.Name == "" || !validToolInput(tool.Arguments) {
				return result, fmt.Errorf("invalid OpenCode Go historical tool call %q", tool.Name)
			}
			wire.ToolCalls = append(wire.ToolCalls, chatCall{ID: tool.ID, Type: "function", Function: chatFunction{Name: tool.Name, Arguments: tool.Arguments}})
		}
		result.Messages = append(result.Messages, wire)
	}
	flushImages()
	for _, tool := range req.Tools {
		result.Tools = append(result.Tools, chatTool{Type: "function", Function: chatToolFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}})
	}
	return result, nil
}

func chatImageParts(text string, images []Image) []any {
	parts := []any{}
	if text != "" {
		parts = append(parts, map[string]any{"type": "text", "text": text})
	}
	for _, image := range images {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + image.MIMEType + ";base64," + image.Data}})
	}
	return parts
}

type chatChunk struct {
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int          `json:"index"`
				ID       string       `json:"id"`
				Function chatFunction `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
		PromptTokensDetails  struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

func readChat(r io.Reader, emit func(Delta)) (*Response, error) {
	response := &Response{}
	var content, reasoningText strings.Builder
	calls := map[int]*ToolCall{}
	err := readSSE(r, func(event string, payload []byte) (bool, error) {
		if string(payload) == "[DONE]" {
			if response.FinishReason == "" {
				return false, fmt.Errorf("OpenCode Go stream ended without finish_reason")
			}
			return true, nil
		}
		var chunk chatChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return false, fmt.Errorf("decode OpenCode Go chat event: %w", err)
		}
		if event == "error" || len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return false, fmt.Errorf("OpenCode Go stream error: %s", payload)
		}
		if chunk.Usage != nil {
			u := chunk.Usage
			cached := u.PromptTokensDetails.CachedTokens
			if cached == 0 {
				cached = u.PromptCacheHitTokens
			}
			response.Usage = Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, CachedTokens: cached, ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.FinishReason != "" {
				response.FinishReason = choice.FinishReason
			}
			delta := choice.Delta
			if delta.Content != "" {
				content.WriteString(delta.Content)
				emit(Delta{Type: "text", Text: delta.Content})
			}
			reasoning := delta.ReasoningContent
			if reasoning == "" {
				reasoning = delta.Reasoning
			}
			if reasoning != "" {
				reasoningText.WriteString(reasoning)
				emit(Delta{Type: "reasoning", Text: reasoning})
			}
			for _, part := range delta.ToolCalls {
				call := calls[part.Index]
				if call == nil {
					call = &ToolCall{}
					calls[part.Index] = call
				}
				call.ID += part.ID
				if part.Function.Name != "" {
					call.Name += part.Function.Name
					emit(Delta{Type: "tool_call", ToolName: call.Name})
				}
				call.Arguments += part.Function.Arguments
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	if len(calls) > 0 && response.FinishReason != "tool_calls" {
		return nil, fmt.Errorf("OpenCode Go tool calls ended with %q instead of tool_calls", response.FinishReason)
	}
	response.Content, response.ReasoningContent = content.String(), reasoningText.String()
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := calls[index]
		if call.ID == "" || call.Name == "" || !validToolInput(call.Arguments) {
			return nil, fmt.Errorf("OpenCode Go returned incomplete tool call at index %d", index)
		}
		response.ToolCalls = append(response.ToolCalls, *call)
	}
	return response, nil
}

func validToolInput(arguments string) bool {
	var input map[string]json.RawMessage
	return json.Unmarshal([]byte(arguments), &input) == nil && input != nil
}
