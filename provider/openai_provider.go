package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const (
	openAIAPIBase     = "https://api.openai.com/v1"
	openAIChatGPTBase = "https://chatgpt.com/backend-api/codex"
)

func init() {
	shared := ProviderRegistration{
		Models:       []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.3-codex", "gpt-5.2-codex", "gpt-5.2"},
		VisionModels: []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.3-codex", "gpt-5.2-codex", "gpt-5.2"},
		ContextWindows: map[string]int{
			"gpt-5.4":       1048576,
			"gpt-5.4-mini":  400000,
			"gpt-5.4-nano":  200000,
			"gpt-5.3-codex": 400000,
			"gpt-5.2-codex": 400000,
			"gpt-5.2":       400000,
		},
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newOpenAIProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	}

	// "openai" — API key auth (env var or static config key).
	apiKeyReg := shared
	apiKeyReg.EnvKey = "OPENAI_API_KEY"
	apiKeyReg.EnvBase = "OPENAI_API_BASE"
	RegisterProvider("openai", apiKeyReg)

	// "openai-oauth" — OAuth token auth (no env var, no API base override).
	oauthReg := shared
	RegisterProvider("openai-oauth", oauthReg)
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
	inputChars := inputChars(req.Messages)

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
		errBody, _ := io.ReadAll(httpResp.Body)
		logger.Error("openai request error", "provider", "openai", "status", httpResp.StatusCode, "body", string(errBody))
		return nil, fmt.Errorf("request failed: %d %s", httpResp.StatusCode, string(errBody))
	}

	resp, err := p.parseSSEStream(httpResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract rate-limit quota from response headers (OAuth mode only).
	if p.accountID != "" {
		resp.Quota = extractQuota(httpResp.Header)
	}

	resp.ProviderLabel = "openai"
	resp.ModelLabel = p.modelName
	if p.accountID != "" {
		resp.ProviderLabel = "openai-oauth"
	}

	logger.Info(
		"openai response",
		"provider", resp.ProviderLabel,
		"modelType", p.modelType,
		"modelName", p.modelName,
		"hasToolCalls", len(resp.ToolCalls) > 0,
		"toolCallCount", len(resp.ToolCalls),
		"promptTokens", resp.Usage.PromptTokens,
		"completionTokens", resp.Usage.CompletionTokens,
		"reasoningTokens", resp.Usage.ReasoningTokens,
		"cachedTokens", resp.Usage.CachedTokens,
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
			content := []map[string]any{
				{"type": "input_text", "text": msg.Content},
			}
			// Process explicit media attachments.
			if len(msg.Media) > 0 {
				_, markers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
				for _, marker := range markers {
					if !strings.HasPrefix(marker.MimeType, "image/") {
						continue // OpenAI Responses API only supports image media
					}
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					content = append(content, map[string]any{
						"type":      "input_image",
						"image_url": "data:" + marker.MimeType + ";base64," + b64,
					})
				}
			}
			input = append(input, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": content,
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
			// Insert OpenAI reasoning items before function_calls if not trimmed.
			// Only include items with type="reasoning" — ReasoningDetails from other
			// providers (Anthropic thinking blocks, Gemini thought_signature) have
			// different formats and must be skipped.
			if !msg.ReasoningTrimmed && len(msg.ReasoningDetails) > 0 {
				var items []json.RawMessage
				if err := json.Unmarshal(msg.ReasoningDetails, &items); err == nil {
					for _, raw := range items {
						var ri map[string]any
						if err := json.Unmarshal(raw, &ri); err == nil {
							if riType, _ := ri["type"].(string); riType == "reasoning" {
								input = append(input, ri)
							}
						}
					}
				}
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
			cleanedText, markers := ParseMediaMarkers(msg.Content)
			hasMedia := len(markers) > 0
			output := []map[string]any{
				{"type": "input_text", "text": cleanedText},
			}
			for _, marker := range markers {
				b64, err := ReadFileAsBase64(marker.FilePath)
				if err != nil {
					continue
				}
				output = append(output, map[string]any{
					"type":      "input_image",
					"image_url": "data:" + marker.MimeType + ";base64," + b64,
				})
			}
			// Process explicit media attachments.
			if len(msg.Media) > 0 {
				_, mediaMarkers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
				for _, marker := range mediaMarkers {
					if !strings.HasPrefix(marker.MimeType, "image/") {
						continue // OpenAI Responses API only supports image media
					}
					b64, err := ReadFileAsBase64(marker.FilePath)
					if err != nil {
						continue
					}
					output = append(output, map[string]any{
						"type":      "input_image",
						"image_url": "data:" + marker.MimeType + ";base64," + b64,
					})
					hasMedia = true
				}
			}
			if hasMedia {
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": msg.ToolCallID,
					"output":  output,
				})
			} else {
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": msg.ToolCallID,
					"output":  msg.Content,
				})
			}
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
		"include": []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{
			"effort":  "high",
			"summary": "auto",
		},
	}
	if p.modelName == "gpt-5.4" {
		body["text"] = map[string]any{"verbosity": "low"}
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n\n")
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	// ChatGPT backend does not support max_output_tokens or temperature.
	// Mini/nano models do not support temperature.
	noTemp := p.accountID != "" || p.modelName == "gpt-5.4-mini" || p.modelName == "gpt-5.4-nano"
	if p.accountID == "" {
		if p.maxTokens > 0 {
			body["max_output_tokens"] = p.maxTokens
		}
		if p.temperature != 0 && !noTemp {
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
	var reasoning strings.Builder
	var reasoningItems []json.RawMessage
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
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					TotalTokens        int `json:"total_tokens"`
					InputTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
					OutputTokensDetails struct {
						ReasoningTokens int `json:"reasoning_tokens"`
					} `json:"output_tokens_details"`
				} `json:"usage"`
				Error *responsesAPIError `json:"error,omitempty"`
			} `json:"response,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue // skip unparseable events
		}

		switch event.Type {
		case "response.output_item.done":
			p.extractOutputItem(event.Item, &content, &toolCalls, &reasoning, &reasoningItems)

		case "response.completed", "response.done":
			usage = Usage{
				PromptTokens:     event.Response.Usage.InputTokens,
				CompletionTokens: event.Response.Usage.OutputTokens,
				TotalTokens:      event.Response.Usage.TotalTokens,
				CachedTokens:     event.Response.Usage.InputTokensDetails.CachedTokens,
				ReasoningTokens:  event.Response.Usage.OutputTokensDetails.ReasoningTokens,
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

	// Pack all reasoning items into a single JSON array for round-trip in ReasoningDetails.
	var reasoningDetails json.RawMessage
	if len(reasoningItems) > 0 {
		reasoningDetails, _ = json.Marshal(reasoningItems)
	}

	return &Response{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		ReasoningDetails: reasoningDetails,
		ToolCalls:        toolCalls,
		Usage:            usage,
	}, nil
}

// extractOutputItem processes a single completed output item from the stream.
func (p *OpenAIProvider) extractOutputItem(item map[string]any, content *strings.Builder, toolCalls *[]ToolCall, reasoning *strings.Builder, reasoningItems *[]json.RawMessage) {
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

	case "reasoning":
		// Extract summary text from reasoning item.
		if summaryArr, ok := item["summary"].([]any); ok {
			for _, s := range summaryArr {
				block, ok := s.(map[string]any)
				if !ok {
					continue
				}
				if blockType, _ := block["type"].(string); blockType == "summary_text" {
					if text, _ := block["text"].(string); text != "" {
						if reasoning.Len() > 0 {
							reasoning.WriteString("\n")
						}
						reasoning.WriteString(text)
					}
				}
			}
		}
		// Preserve the complete reasoning item as raw JSON for round-trip.
		if raw, err := json.Marshal(item); err == nil {
			*reasoningItems = append(*reasoningItems, json.RawMessage(raw))
		}
	}
}

type responsesAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// extractQuota parses x-ratelimit-* headers into a Quota snapshot.
// Returns nil if no rate-limit headers are present.
func extractQuota(h http.Header) *Quota {
	lr := headerInt(h, "X-Ratelimit-Limit-Requests")
	lt := headerInt(h, "X-Ratelimit-Limit-Tokens")
	rr := headerInt(h, "X-Ratelimit-Remaining-Requests")
	rt := headerInt(h, "X-Ratelimit-Remaining-Tokens")
	if lr == 0 && lt == 0 && rr == 0 && rt == 0 {
		return nil
	}
	return &Quota{
		LimitRequests:     lr,
		LimitTokens:       lt,
		RemainingRequests: rr,
		RemainingTokens:   rt,
		ResetRequests:     h.Get("X-Ratelimit-Reset-Requests"),
		ResetTokens:       h.Get("X-Ratelimit-Reset-Tokens"),
		UpdatedAt:         time.Now(),
	}
}

func headerInt(h http.Header, key string) int {
	v := h.Get(key)
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}
