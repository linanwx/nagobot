// Package opencodego is a self-contained inference client for OpenCode Go.
// Its wire formats, model catalog, transport and streaming parser belong to
// this gateway, independently of native model providers and their SDKs.
package opencodego

import "encoding/json"

type Request struct {
	Model     string
	Effort    string
	MaxTokens int
	Messages  []Message
	Tools     []Tool
}

type Message struct {
	Role, Content, ReasoningContent string
	ReasoningDetails                json.RawMessage
	Images                          []Image
	ToolCalls                       []ToolCall
	ToolCallID                      string
}

type Image struct{ MIMEType, Data string }
type Tool struct {
	Name, Description string
	Parameters        map[string]any
}
type ToolCall struct{ ID, Name, Arguments string }
type Response struct {
	Content, ReasoningContent string
	ReasoningDetails          json.RawMessage
	ToolCalls                 []ToolCall
	Usage                     Usage
	FinishReason              string
}
type Usage struct{ PromptTokens, CompletionTokens, TotalTokens, CachedTokens, ReasoningTokens, CacheWriteTokens int }
type Delta struct{ Type, Text, ToolName string }

type Model struct {
	ID            string
	ContextWindow int
	Vision        bool
	Efforts       []string
}

var catalog = []Model{
	{"glm-5.3-flash", 1000000, true, []string{"low", "high", "max"}},
	{"glm-5.3", 1000000, false, []string{"low", "high", "max"}},
	{"deepseek-v4.1-flash", 1000000, true, []string{"low", "high", "max"}},
	{"deepseek-v4-pro", 1000000, false, []string{"high", "max"}},
	{"claude-haiku-5-5", 1000000, true, []string{"low", "medium", "high", "xhigh", "max"}},
}

func Models() []Model {
	models := append([]Model(nil), catalog...)
	for i := range models {
		models[i].Efforts = append([]string(nil), models[i].Efforts...)
	}
	return models
}

func ModelInfo(id string) (Model, bool) {
	for _, model := range Models() {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}
