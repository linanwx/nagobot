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

const mimoAPIBase = "https://api.xiaomimimo.com/v1"

func init() {
	RegisterProvider("mimo", ProviderRegistration{
		Models: []string{"mimo-v2.5-pro", "mimo-v2.5", "mimo-v2-pro", "mimo-v2-flash", "mimo-v2-omni"},
		ContextWindows: map[string]int{
			"mimo-v2.5-pro": 1048576,
			"mimo-v2.5":     1048576,
			"mimo-v2-pro":   1048576,
			"mimo-v2-flash": 131072,
			"mimo-v2-omni":  262144,
		},
		EnvKey:  "MIMO_API_KEY",
		EnvBase: "MIMO_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newMiMoProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// ---------- JSON wire types ----------

type mmRequest struct {
	Model         string        `json:"model"`
	Messages      []mmMessage   `json:"messages"`
	Tools         []ToolDef     `json:"tools,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions *mmStreamOpts `json:"stream_options,omitempty"`
	Thinking      *mmThinking   `json:"thinking,omitempty"`
}

type mmStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type mmThinking struct {
	Type string `json:"type"` // "enabled" | "disabled"
}

type mmMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"` // nullable
	ReasoningContent *string    `json:"reasoning_content,omitempty"` // assistant only; required for multi-turn thinking mode
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
}

type mmUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	PromptTokensDetails     struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// Streaming chunk.
type mmChunk struct {
	Choices []mmChunkChoice `json:"choices"`
	Usage   *mmUsage        `json:"usage"`
}

type mmChunkChoice struct {
	Delta        mmDelta `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type mmDelta struct {
	Content          string      `json:"content"`
	ReasoningContent string      `json:"reasoning_content"`
	ToolCalls        []mmDeltaTC `json:"tool_calls"`
}

type mmDeltaTC struct {
	Index    int           `json:"index"`
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function mmDeltaTCFunc `json:"function"`
}

type mmDeltaTCFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type mmErrorResp struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ---------- provider ----------

// MiMoProvider implements the Provider interface for Xiaomi MiMo direct API.
type MiMoProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      *http.Client
}

func newMiMoProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *MiMoProvider {
	if modelName == "" {
		modelName = modelType
	}
	if apiBase == "" {
		apiBase = mimoAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")
	apiBase = strings.TrimSuffix(apiBase, "/chat/completions")
	apiBase = strings.TrimSuffix(apiBase, "/v1")
	apiBase = apiBase + "/v1"

	return &MiMoProvider{
		apiKey:      apiKey,
		apiBase:     apiBase,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      &http.Client{},
	}
}

func (p *MiMoProvider) endpoint() string {
	return p.apiBase + "/chat/completions"
}

// reasoningDefaultsOn reports whether this MiMo model has reasoning enabled by
// default. v2.5 Pro and v2.5 both support reasoning; v2 pro and omni do; v2 flash does not.
func mimoReasoningDefaultsOn(modelType string) bool {
	mt := strings.TrimSpace(modelType)
	return mt == "mimo-v2.5-pro" || mt == "mimo-v2.5" || mt == "mimo-v2-pro" || mt == "mimo-v2-omni"
}

// Chat sends a chat completion request to MiMo.
func (p *MiMoProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)
	thinkingEnabled := mimoReasoningDefaultsOn(p.modelType)

	logger.Info(
		"mimo request",
		"provider", "mimo",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"thinkingEnabled", thinkingEnabled,
		"streaming", true,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	mmReq := p.buildRequest(req, thinkingEnabled, true)
	return p.chatStream(ctx, mmReq, start)
}

func (p *MiMoProvider) buildRequest(req *Request, thinkingEnabled, streaming bool) mmRequest {
	r := mmRequest{
		Model:    p.modelName,
		Messages: toMMMessages(req.Messages),
		Tools:    req.Tools,
		Stream:   streaming,
	}
	if p.maxTokens > 0 {
		r.MaxTokens = p.maxTokens
	}
	// MiMo accepts temperature alongside reasoning; pass through when configured.
	if p.temperature != 0 {
		t := p.temperature
		r.Temperature = &t
	}
	// Make reasoning intent explicit so the request is robust against future
	// changes to server-side defaults: pro/omni → enabled, flash → disabled.
	if thinkingEnabled {
		r.Thinking = &mmThinking{Type: "enabled"}
	} else {
		r.Thinking = &mmThinking{Type: "disabled"}
	}
	if streaming {
		r.StreamOptions = &mmStreamOpts{IncludeUsage: true}
	}
	return r
}

func (p *MiMoProvider) doPost(ctx context.Context, body any) (*http.Response, error) {
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

// chatStream handles streaming completion with SSE parsing.
func (p *MiMoProvider) chatStream(ctx context.Context, mmReq mmRequest, start time.Time) (ChatResult, error) {
	httpResp, err := p.doPost(ctx, mmReq)
	if err != nil {
		logger.Error("mimo streaming request error", "provider", "mimo", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		body, _ := io.ReadAll(httpResp.Body)
		var apiErr mmErrorResp
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("mimo API error (%d): %s", httpResp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("mimo API error (%d): %s", httpResp.StatusCode, string(body))
	}

	resp := &Response{
		ProviderLabel: "mimo",
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
			usage            mmUsage
			finishReason     string
		)

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

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

			var chunk mmChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				logger.Debug("mimo stream chunk parse skip", "err", err)
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
			logger.Error("mimo stream read error", "err", err)
			adapter.SetError(fmt.Errorf("stream read error: %w", err))
		}

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
		finalContent = resolveContentWithReasoningFallback(finalContent, reasoningText, "mimo", toolCalls)

		logger.Info(
			"mimo streaming response",
			"provider", "mimo",
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
			"cachedTokens", usage.PromptTokensDetails.CachedTokens,
			"outputChars", len(finalContent),
			"latencyMs", time.Since(start).Milliseconds(),
		)

		resp.Content = finalContent
		resp.ReasoningContent = reasoningText
		resp.ToolCalls = toolCalls
		resp.Usage = Usage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			CachedTokens:     usage.PromptTokensDetails.CachedTokens,
			ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		}
	}()

	return adapter.Result(), nil
}

// ---------- helpers ----------

func toMMMessages(messages []Message) []mmMessage {
	out := make([]mmMessage, 0, len(messages))
	for _, m := range messages {
		dm := mmMessage{
			Role:       m.Role,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		switch m.Role {
		case "assistant":
			if m.Content != "" {
				dm.Content = &m.Content
			}
			// MiMo requires prior reasoning_content in multi-turn thinking mode.
			// Per platform.xiaomimimo.com: "keep all previous reasoning_content
			// in the messages array for each subsequent request". Compression
			// already clears ReasoningContent for trimmed messages via
			// ApplyCompressedMessage, so pass whatever remains.
			if m.ReasoningContent != "" {
				rc := m.ReasoningContent
				dm.ReasoningContent = &rc
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]ToolCall, len(m.ToolCalls))
				copy(tcs, m.ToolCalls)
				for j := range tcs {
					if !json.Valid([]byte(tcs[j].Function.Arguments)) {
						tcs[j].Function.Arguments = "{}"
					}
				}
				dm.ToolCalls = tcs
			}
		default:
			dm.Content = &m.Content
		}
		out = append(out, dm)
	}
	return out
}
