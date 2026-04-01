package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const deepSeekAPIBase = "https://api.deepseek.com"

func init() {
	RegisterProvider("deepseek", ProviderRegistration{
		Models: []string{"deepseek-reasoner", "deepseek-chat"},
		ContextWindows: map[string]int{
			"deepseek-reasoner": 128000,
			"deepseek-chat":     128000,
		},
		EnvKey:  "DEEPSEEK_API_KEY",
		EnvBase: "DEEPSEEK_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newDeepSeekProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// ---------- JSON wire types ----------

type dsRequest struct {
	Model         string        `json:"model"`
	Messages      []dsMessage   `json:"messages"`
	Tools         []ToolDef     `json:"tools,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions *dsStreamOpts `json:"stream_options,omitempty"`
	Thinking      *dsThinking   `json:"thinking,omitempty"`
}

type dsStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type dsThinking struct {
	Type string `json:"type"` // "enabled"
}

type dsMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`                     // nullable
	ReasoningContent *string    `json:"reasoning_content,omitempty"` // assistant only
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`        // assistant only
	ToolCallID       string     `json:"tool_call_id,omitempty"`      // tool only
	Name             string     `json:"name,omitempty"`
}

// Non-streaming response.
type dsResponse struct {
	Choices []dsChoice `json:"choices"`
	Usage   dsUsage    `json:"usage"`
}

type dsChoice struct {
	Message      dsRespMsg `json:"message"`
	FinishReason string    `json:"finish_reason"`
}

type dsRespMsg struct {
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
}

type dsUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	PromptCacheHitTokens    int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int `json:"prompt_cache_miss_tokens"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// Streaming chunk.
type dsChunk struct {
	Choices []dsChunkChoice `json:"choices"`
	Usage   *dsUsage        `json:"usage"`
}

type dsChunkChoice struct {
	Delta        dsDelta `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type dsDelta struct {
	Content          string      `json:"content"`
	ReasoningContent string      `json:"reasoning_content"`
	ToolCalls        []dsDeltaTC `json:"tool_calls"`
}

type dsDeltaTC struct {
	Index    int           `json:"index"`
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function dsDeltaTCFunc `json:"function"`
}

type dsDeltaTCFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type dsErrorResp struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// streamToolCallAcc accumulates incremental tool call deltas.
type streamToolCallAcc struct {
	id   string
	typ  string
	name string
	args strings.Builder
}

// ---------- provider ----------

// DeepSeekProvider implements the Provider interface using raw HTTP.
type DeepSeekProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      *http.Client
}

func newDeepSeekProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *DeepSeekProvider {
	if modelName == "" {
		modelName = modelType
	}
	if apiBase == "" {
		apiBase = deepSeekAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")
	apiBase = strings.TrimSuffix(apiBase, "/chat/completions")
	apiBase = strings.TrimSuffix(apiBase, "/v1")

	return &DeepSeekProvider{
		apiKey:      apiKey,
		apiBase:     apiBase,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      &http.Client{},
	}
}

func (p *DeepSeekProvider) endpoint() string {
	return p.apiBase + "/chat/completions"
}

// Chat sends a chat completion request to DeepSeek.
func (p *DeepSeekProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)
	thinkingEnabled := strings.TrimSpace(p.modelType) == "deepseek-reasoner"

	logger.Info(
		"deepseek request",
		"provider", "deepseek",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"thinkingEnabled", thinkingEnabled,
		"streaming", true,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	dsReq := p.buildRequest(req, thinkingEnabled, true)
	return p.chatStream(ctx, dsReq, start)
}

func (p *DeepSeekProvider) buildRequest(req *Request, thinkingEnabled, streaming bool) dsRequest {
	r := dsRequest{
		Model:    p.modelName,
		Messages: toDSMessages(req.Messages),
		Tools:    req.Tools,
		Stream:   streaming,
	}
	if p.maxTokens > 0 {
		r.MaxTokens = p.maxTokens
	}
	if p.temperature != 0 && !thinkingEnabled {
		t := p.temperature
		r.Temperature = &t
	}
	if thinkingEnabled {
		r.Thinking = &dsThinking{Type: "enabled"}
	}
	if streaming {
		r.StreamOptions = &dsStreamOpts{IncludeUsage: true}
	}
	return r
}

func (p *DeepSeekProvider) doPost(ctx context.Context, body any) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	return p.client.Do(httpReq)
}

// chatSync handles non-streaming completion.
func (p *DeepSeekProvider) chatSync(ctx context.Context, dsReq dsRequest, start time.Time) (ChatResult, error) {
	httpResp, err := p.doPost(ctx, dsReq)
	if err != nil {
		logger.Error("deepseek request error", "provider", "deepseek", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		var apiErr dsErrorResp
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, string(body))
	}

	var resp dsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := resp.Choices[0]
	finalContent := choice.Message.Content
	reasoningText := choice.Message.ReasoningContent
	finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "deepseek", choice.Message.ToolCalls)

	u := resp.Usage
	logger.Info(
		"deepseek response",
		"provider", "deepseek",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", choice.FinishReason,
		"reasoningInResponse", u.CompletionTokensDetails.ReasoningTokens > 0 || strings.TrimSpace(reasoningText) != "",
		"hasToolCalls", len(choice.Message.ToolCalls) > 0,
		"toolCallCount", len(choice.Message.ToolCalls),
		"promptTokens", u.PromptTokens,
		"completionTokens", u.CompletionTokens,
		"reasoningTokens", u.CompletionTokensDetails.ReasoningTokens,
		"totalTokens", u.TotalTokens,
		"promptCacheHitTokens", u.PromptCacheHitTokens,
		"promptCacheMissTokens", u.PromptCacheMissTokens,
		"outputChars", len(finalContent),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return NewBasicResult(&Response{
		Content:          finalContent,
		ReasoningContent: reasoningText,
		ToolCalls:        choice.Message.ToolCalls,
		Usage: Usage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
			CachedTokens:     u.PromptCacheHitTokens,
			ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
		},
		ProviderLabel: "deepseek",
		ModelLabel:    p.modelName,
	}), nil
}

// chatStream handles streaming completion with SSE parsing.
func (p *DeepSeekProvider) chatStream(ctx context.Context, dsReq dsRequest, start time.Time) (ChatResult, error) {
	httpResp, err := p.doPost(ctx, dsReq)
	if err != nil {
		logger.Error("deepseek streaming request error", "provider", "deepseek", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		body, _ := io.ReadAll(httpResp.Body)
		var apiErr dsErrorResp
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("deepseek API error (%d): %s", httpResp.StatusCode, string(body))
	}

	resp := &Response{
		ProviderLabel: "deepseek",
		ModelLabel:    p.modelName,
	}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer httpResp.Body.Close()
		defer adapter.Finish()

		var (
			content          strings.Builder
			reasoning        strings.Builder
			toolCallAcc      = map[int]*streamToolCallAcc{}
			toolCallSignaled bool
			usage            dsUsage
			finishReason     string
		)

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			// Skip SSE comments (: keep-alive), empty lines, retry directives.
			if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "retry:") {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := line[6:]
			if data == "[DONE]" {
				break
			}

			var chunk dsChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				logger.Debug("deepseek stream chunk parse skip", "err", err)
				continue
			}

			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta

			if delta.ReasoningContent != "" {
				reasoning.WriteString(delta.ReasoningContent)
			}
			if delta.Content != "" {
				content.WriteString(delta.Content)
				adapter.EmitText(delta.Content)
			}

			// Accumulate tool calls by index.
			if len(delta.ToolCalls) > 0 && !toolCallSignaled {
				toolCallSignaled = true
				if name := delta.ToolCalls[0].Function.Name; name != "" {
					adapter.EmitToolCall(name)
				}
			}
			for _, tc := range delta.ToolCalls {
				acc, ok := toolCallAcc[tc.Index]
				if !ok {
					acc = &streamToolCallAcc{}
					toolCallAcc[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Type != "" {
					acc.typ = tc.Type
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}

			if chunk.Choices[0].FinishReason != nil {
				finishReason = *chunk.Choices[0].FinishReason
			}
		}

		if err := scanner.Err(); err != nil {
			logger.Error("deepseek stream read error", "err", err)
			adapter.SetError(fmt.Errorf("stream read error: %w", err))
		}

		// Assemble tool calls from accumulated deltas.
		var toolCalls []ToolCall
		for i := 0; i < len(toolCallAcc); i++ {
			tc := toolCallAcc[i]
			if tc == nil {
				continue
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   tc.id,
				Type: tc.typ,
				Function: FunctionCall{
					Name:      tc.name,
					Arguments: tc.args.String(),
				},
			})
		}

		finalContent := content.String()
		reasoningText := reasoning.String()
		finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "deepseek", toolCalls)

		logger.Info(
			"deepseek streaming response",
			"provider", "deepseek",
			"modelType", p.modelType,
			"modelName", p.modelName,
			"finishReason", finishReason,
			"reasoningInResponse", usage.CompletionTokensDetails.ReasoningTokens > 0 || strings.TrimSpace(reasoningText) != "",
			"hasToolCalls", len(toolCalls) > 0,
			"toolCallCount", len(toolCalls),
			"promptTokens", usage.PromptTokens,
			"completionTokens", usage.CompletionTokens,
			"reasoningTokens", usage.CompletionTokensDetails.ReasoningTokens,
			"totalTokens", usage.TotalTokens,
			"promptCacheHitTokens", usage.PromptCacheHitTokens,
			"promptCacheMissTokens", usage.PromptCacheMissTokens,
			"outputChars", len(finalContent),
			"latencyMs", time.Since(start).Milliseconds(),
		)

		// Fill resp fields before Finish closes the channel.
		resp.Content = finalContent
		resp.ReasoningContent = reasoningText
		resp.ToolCalls = toolCalls
		resp.Usage = Usage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			CachedTokens:     usage.PromptCacheHitTokens,
			ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		}
	}()

	return adapter.Result(), nil
}

// ---------- helpers ----------

func toDSMessages(messages []Message) []dsMessage {
	// DeepSeek only allows reasoning_content on the last assistant message.
	lastAssistantIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			lastAssistantIdx = i
			break
		}
	}

	out := make([]dsMessage, 0, len(messages))
	for i, m := range messages {
		dm := dsMessage{
			Role:       m.Role,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		switch m.Role {
		case "assistant":
			if m.Content != "" {
				dm.Content = &m.Content
			}
			// DeepSeek requires reasoning_content on ALL assistant messages.
			// Only the last assistant message (without tool_calls) may carry
			// actual reasoning; all others get an empty string.
			empty := ""
			if i == lastAssistantIdx && m.ReasoningContent != "" && len(m.ToolCalls) == 0 {
				dm.ReasoningContent = &m.ReasoningContent
			} else {
				dm.ReasoningContent = &empty
			}
			dm.ToolCalls = m.ToolCalls
		default: // system, user, tool
			dm.Content = &m.Content
		}
		out = append(out, dm)
	}
	return out
}
