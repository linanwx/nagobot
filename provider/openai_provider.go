package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const (
	openAIAPIBase     = "https://api.openai.com/v1"
	openAIChatGPTBase = "https://chatgpt.com/backend-api/codex"
)

func init() {
	RegisterProvider("openai", ProviderRegistration{
		Models:       []string{"gpt-5.2"},
		VisionModels: []string{"gpt-5.2"},
		EnvKey:       "OPENAI_API_KEY",
		EnvBase:      "OPENAI_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newOpenAIProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// OpenAIProvider implements the Provider interface using the OpenAI Responses API.
type OpenAIProvider struct {
	apiKey      string
	baseURL     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
	accountID   string // ChatGPT account ID from OAuth id_token
}

// SetAccountID sets the ChatGPT account ID for OAuth-based requests.
func (p *OpenAIProvider) SetAccountID(id string) {
	p.accountID = id
}

func newOpenAIProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *OpenAIProvider {
	if modelName == "" {
		modelName = modelType
	}
	baseURL := strings.TrimSpace(apiBase)
	if baseURL == "" {
		baseURL = openAIAPIBase
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &OpenAIProvider{
		apiKey:      apiKey,
		baseURL:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// Chat sends a request to the OpenAI Responses API (streaming).
func (p *OpenAIProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := openRouterInputChars(req.Messages)

	logger.Info(
		"openai request",
		"provider", "openai",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// Use ChatGPT backend when authenticated via OAuth (account ID present).
	base := p.baseURL
	if p.accountID != "" {
		base = openAIChatGPTBase
	}
	url := base + "/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	if p.accountID != "" {
		httpReq.Header.Set("ChatGPT-Account-ID", p.accountID)
	}

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("openai request error", "provider", "openai", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		buf.ReadFrom(httpResp.Body)
		errBody := buf.String()
		logger.Error("openai request error", "provider", "openai", "status", httpResp.StatusCode, "body", errBody)
		return nil, fmt.Errorf("request failed: %d %s", httpResp.StatusCode, errBody)
	}

	resp, err := p.parseSSEStream(httpResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	logger.Info(
		"openai response",
		"provider", "openai",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"hasToolCalls", len(resp.ToolCalls) > 0,
		"toolCallCount", len(resp.ToolCalls),
		"promptTokens", resp.Usage.PromptTokens,
		"completionTokens", resp.Usage.CompletionTokens,
		"totalTokens", resp.Usage.TotalTokens,
		"outputChars", len(resp.Content),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return resp, nil
}

// buildRequestBody converts internal Request to Responses API JSON.
func (p *OpenAIProvider) buildRequestBody(req *Request) ([]byte, error) {
	// Extract system messages into instructions.
	var instructions []string
	var input []map[string]any

	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			instructions = append(instructions, msg.Content)

		case "user":
			input = append(input, map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": msg.Content},
				},
			})

		case "assistant":
			if msg.Content != "" {
				input = append(input, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": msg.Content},
					},
				})
			}
			for _, tc := range msg.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}

		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		}
	}

	// Convert tools to Responses API format (flat structure).
	var tools []map[string]any
	for _, t := range req.Tools {
		tool := map[string]any{
			"type":       "function",
			"name":       t.Function.Name,
			"parameters": t.Function.Parameters,
		}
		if t.Function.Description != "" {
			tool["description"] = t.Function.Description
		}
		tools = append(tools, tool)
	}

	body := map[string]any{
		"model":  p.modelName,
		"input":  input,
		"stream": true,
		"store":  false,
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n\n")
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	// ChatGPT backend does not support max_output_tokens or temperature.
	if p.accountID == "" {
		if p.maxTokens > 0 {
			body["max_output_tokens"] = p.maxTokens
		}
		if p.temperature != 0 {
			body["temperature"] = p.temperature
		}
	}

	return json.Marshal(body)
}

// parseSSEStream reads an SSE event stream and assembles the complete response.
// We collect response.output_item.done events for output items and
// response.completed for usage data.
func (p *OpenAIProvider) parseSSEStream(httpResp *http.Response) (*Response, error) {
	var content strings.Builder
	var toolCalls []ToolCall
	var usage Usage

	scanner := bufio.NewScanner(httpResp.Body)
	// Increase buffer for large events.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {json}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type     string         `json:"type"`
			Item     map[string]any `json:"item,omitempty"`
			Response struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
				Error *responsesAPIError `json:"error,omitempty"`
			} `json:"response,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue // skip unparseable events
		}

		switch event.Type {
		case "response.output_item.done":
			p.extractOutputItem(event.Item, &content, &toolCalls)

		case "response.completed", "response.done":
			usage = Usage{
				PromptTokens:     event.Response.Usage.InputTokens,
				CompletionTokens: event.Response.Usage.OutputTokens,
				TotalTokens:      event.Response.Usage.TotalTokens,
			}

		case "response.failed":
			errInfo := event.Response.Error
			if errInfo != nil {
				return nil, fmt.Errorf("API error [%s]: %s", errInfo.Code, errInfo.Message)
			}
			return nil, fmt.Errorf("API returned response.failed")
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading SSE stream: %w", err)
	}

	return &Response{
		Content:   content.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
	}, nil
}

// extractOutputItem processes a single completed output item from the stream.
func (p *OpenAIProvider) extractOutputItem(item map[string]any, content *strings.Builder, toolCalls *[]ToolCall) {
	if item == nil {
		return
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		contentArr, _ := item["content"].([]any)
		for _, c := range contentArr {
			block, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType == "output_text" {
				if text, _ := block["text"].(string); text != "" {
					if content.Len() > 0 {
						content.WriteString("\n")
					}
					content.WriteString(text)
				}
			}
		}

	case "function_call":
		callID, _ := item["call_id"].(string)
		name, _ := item["name"].(string)
		args, _ := item["arguments"].(string)
		*toolCalls = append(*toolCalls, ToolCall{
			ID:   callID,
			Type: "function",
			Function: FunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
}

type responsesAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
