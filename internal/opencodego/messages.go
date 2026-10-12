package opencodego

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type messagesWireRequest struct {
	Model        string                `json:"model"`
	System       string                `json:"system,omitempty"`
	Messages     []messagesMessage     `json:"messages"`
	Tools        []messagesTool        `json:"tools,omitempty"`
	MaxTokens    int                   `json:"max_tokens"`
	Stream       bool                  `json:"stream"`
	OutputConfig *messagesOutputConfig `json:"output_config,omitempty"`
	Thinking     messagesThinking      `json:"thinking"`
}
type messagesOutputConfig struct {
	Effort string `json:"effort"`
}
type messagesThinking struct {
	Type         string               `json:"type"`
	BlockBinding messagesBlockBinding `json:"block_binding"`
}
type messagesBlockBinding struct {
	PrefixMismatchBehavior string `json:"prefix_mismatch_behavior"`
}
type messagesMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}
type messagesTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

func block(value any) json.RawMessage { encoded, _ := json.Marshal(value); return encoded }

func messagesRequest(req Request) (messagesWireRequest, error) {
	result := messagesWireRequest{Model: req.Model, MaxTokens: req.MaxTokens, Stream: true,
		Thinking: messagesThinking{Type: "adaptive", BlockBinding: messagesBlockBinding{PrefixMismatchBehavior: "drop_block"}}}
	if result.MaxTokens <= 0 {
		result.MaxTokens = 128000
	}
	if req.Effort != "" {
		result.OutputConfig = &messagesOutputConfig{Effort: req.Effort}
	}
	var systems []string
	for _, message := range req.Messages {
		if message.Role == "system" {
			systems = append(systems, message.Content)
			continue
		}
		role := message.Role
		content := []json.RawMessage{}
		if role == "tool" {
			if message.ToolCallID == "" {
				return result, fmt.Errorf("OpenCode Go tool result has no tool call ID")
			}
			role = "user"
			var resultContent any = message.Content
			if len(message.Images) > 0 {
				parts := []json.RawMessage{}
				if message.Content != "" {
					parts = append(parts, block(map[string]string{"type": "text", "text": message.Content}))
				}
				for _, image := range message.Images {
					parts = append(parts, messagesImage(image))
				}
				resultContent = parts
			}
			content = append(content, block(map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": resultContent}))
		} else {
			if role != "user" && role != "assistant" {
				return result, fmt.Errorf("unsupported OpenCode Go Messages role %q", role)
			}
			if role == "assistant" && len(message.ReasoningDetails) > 0 {
				var blocks []json.RawMessage
				if err := json.Unmarshal(message.ReasoningDetails, &blocks); err != nil {
					return result, fmt.Errorf("decode OpenCode Go signed reasoning: %w", err)
				}
				for _, raw := range blocks {
					if validThinkingBlock(raw) {
						content = append(content, raw)
					}
				}
			}
			if message.Content != "" {
				content = append(content, block(map[string]string{"type": "text", "text": message.Content}))
			}
			for _, image := range message.Images {
				content = append(content, messagesImage(image))
			}
			for _, call := range message.ToolCalls {
				if role != "assistant" || call.ID == "" || call.Name == "" || !validToolInput(call.Arguments) {
					return result, fmt.Errorf("invalid OpenCode Go tool input for %s", call.Name)
				}
				content = append(content, block(map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": json.RawMessage(call.Arguments)}))
			}
		}
		// Anthropic requires alternating roles; adjacent user/tool records are
		// one user turn with multiple content blocks.
		if len(content) == 0 {
			continue
		}
		if len(result.Messages) > 0 && result.Messages[len(result.Messages)-1].Role == role {
			last := &result.Messages[len(result.Messages)-1]
			last.Content = append(last.Content, content...)
		} else {
			result.Messages = append(result.Messages, messagesMessage{Role: role, Content: content})
		}
	}
	result.System = strings.Join(systems, "\n\n")
	for _, tool := range req.Tools {
		result.Tools = append(result.Tools, messagesTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters})
	}
	return result, nil
}

func messagesImage(image Image) json.RawMessage {
	return block(map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": image.MIMEType, "data": image.Data}})
}

func validThinkingBlock(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var kind string
	_ = json.Unmarshal(value["type"], &kind)
	switch kind {
	case "thinking":
		var text, signature string
		return json.Unmarshal(value["thinking"], &text) == nil && json.Unmarshal(value["signature"], &signature) == nil && signature != ""
	case "redacted_thinking":
		var data string
		return json.Unmarshal(value["data"], &data) == nil && data != ""
	}
	return false
}

type messagesUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}
type messagesEvent struct {
	Type    string          `json:"type"`
	Index   int             `json:"index"`
	Error   json.RawMessage `json:"error"`
	Message struct {
		Usage messagesUsage `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage messagesUsage `json:"usage"`
}
type messagesAccumulatedBlock struct {
	fields    map[string]any
	arguments strings.Builder
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	partial   bool
	stopped   bool
}

func readMessages(r io.Reader, emit func(Delta)) (*Response, error) {
	response := &Response{}
	var content, reasoningText strings.Builder
	blocks := map[int]*messagesAccumulatedBlock{}
	updateUsage := func(u messagesUsage) {
		if u.InputTokens != nil {
			response.Usage.PromptTokens = *u.InputTokens
		}
		if u.OutputTokens != nil {
			response.Usage.CompletionTokens = *u.OutputTokens
		}
		if u.CacheReadInputTokens != nil {
			response.Usage.CachedTokens = *u.CacheReadInputTokens
		}
		if u.CacheCreationInputTokens != nil {
			response.Usage.CacheWriteTokens = *u.CacheCreationInputTokens
		}
	}
	err := readSSE(r, func(kind string, payload []byte) (bool, error) {
		var event messagesEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return false, fmt.Errorf("decode OpenCode Go Messages event: %w", err)
		}
		if event.Type == "error" || kind == "error" || len(event.Error) > 0 && string(event.Error) != "null" {
			return false, fmt.Errorf("OpenCode Go Messages stream error: %s", payload)
		}
		switch event.Type {
		case "message_start":
			updateUsage(event.Message.Usage)
		case "content_block_start":
			if _, exists := blocks[event.Index]; exists {
				return false, fmt.Errorf("duplicate OpenCode Go content block %d", event.Index)
			}
			var fields map[string]any
			if err := json.Unmarshal(event.ContentBlock, &fields); err != nil || fields == nil {
				return false, fmt.Errorf("invalid OpenCode Go content block %d", event.Index)
			}
			current := &messagesAccumulatedBlock{fields: fields}
			if value, ok := fields["text"].(string); ok {
				current.text.WriteString(value)
			}
			if value, ok := fields["thinking"].(string); ok {
				current.thinking.WriteString(value)
			}
			if value, ok := fields["signature"].(string); ok {
				current.signature.WriteString(value)
			}
			blocks[event.Index] = current
			switch fields["type"] {
			case "text":
				if text, _ := fields["text"].(string); text != "" {
					content.WriteString(text)
					emit(Delta{Type: "text", Text: text})
				}
			case "thinking":
				if text, _ := fields["thinking"].(string); text != "" {
					reasoningText.WriteString(text)
					emit(Delta{Type: "reasoning", Text: text})
				}
			case "tool_use":
				name, _ := fields["name"].(string)
				emit(Delta{Type: "tool_call", ToolName: name})
			}
		case "content_block_delta":
			current := blocks[event.Index]
			if current == nil || current.stopped {
				return false, fmt.Errorf("OpenCode Go delta without active block %d", event.Index)
			}
			switch event.Delta.Type {
			case "text_delta":
				current.text.WriteString(event.Delta.Text)
				content.WriteString(event.Delta.Text)
				emit(Delta{Type: "text", Text: event.Delta.Text})
			case "thinking_delta":
				current.thinking.WriteString(event.Delta.Thinking)
				reasoningText.WriteString(event.Delta.Thinking)
				emit(Delta{Type: "reasoning", Text: event.Delta.Thinking})
			case "signature_delta":
				current.signature.WriteString(event.Delta.Signature)
			case "input_json_delta":
				current.arguments.WriteString(event.Delta.PartialJSON)
				current.partial = true
			}
		case "content_block_stop":
			current := blocks[event.Index]
			if current == nil || current.stopped {
				return false, fmt.Errorf("OpenCode Go stop without active block %d", event.Index)
			}
			current.stopped = true
		case "message_delta":
			if event.Delta.StopReason != "" {
				response.FinishReason = event.Delta.StopReason
			}
			updateUsage(event.Usage)
		case "message_stop":
			if response.FinishReason == "" {
				return false, fmt.Errorf("OpenCode Go Messages stream ended without stop_reason")
			}
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	response.Content, response.ReasoningContent = content.String(), reasoningText.String()
	indexes := make([]int, 0, len(blocks))
	for index := range blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var reasoning []json.RawMessage
	for _, index := range indexes {
		current := blocks[index]
		if !current.stopped {
			return nil, fmt.Errorf("OpenCode Go content block %d truncated", index)
		}
		switch current.fields["type"] {
		case "thinking", "redacted_thinking":
			if current.fields["type"] == "thinking" {
				current.fields["thinking"], current.fields["signature"] = current.thinking.String(), current.signature.String()
			}
			raw := block(current.fields)
			if !validThinkingBlock(raw) {
				return nil, fmt.Errorf("OpenCode Go returned invalid signed thinking block %d", index)
			}
			reasoning = append(reasoning, raw)
		case "tool_use":
			if response.FinishReason != "tool_use" {
				return nil, fmt.Errorf("OpenCode Go tool use ended with %q instead of tool_use", response.FinishReason)
			}
			id, _ := current.fields["id"].(string)
			name, _ := current.fields["name"].(string)
			arguments := current.arguments.String()
			if !current.partial {
				arguments = string(block(current.fields["input"]))
			}
			if id == "" || name == "" || !validToolInput(arguments) {
				return nil, fmt.Errorf("OpenCode Go returned incomplete tool use %d", index)
			}
			response.ToolCalls = append(response.ToolCalls, ToolCall{ID: id, Name: name, Arguments: arguments})
		}
	}
	if len(reasoning) > 0 {
		response.ReasoningDetails = block(reasoning)
	}
	// Anthropic input_tokens excludes cached input, unlike Chat Completions.
	response.Usage.PromptTokens += response.Usage.CachedTokens + response.Usage.CacheWriteTokens
	response.Usage.TotalTokens = response.Usage.PromptTokens + response.Usage.CompletionTokens
	return response, nil
}
