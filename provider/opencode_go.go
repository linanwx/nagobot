package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/internal/opencodego"
	"github.com/linanwx/nagobot/logger"
)

func init() {
	var models, vision []string
	windows := map[string]int{}
	for _, model := range opencodego.Models() {
		variants := []string{model.ID}
		for _, effort := range model.Efforts {
			variants = append(variants, model.ID+"["+effort+"]")
		}
		for _, variant := range variants {
			models = append(models, variant)
			windows[variant] = model.ContextWindow
			if model.Vision {
				vision = append(vision, variant)
			}
		}
	}
	RegisterProvider("opencode-go", ProviderRegistration{
		Models: models, VisionModels: vision, ContextWindows: windows,
		EnvKey: "OPENCODE_GO_API_KEY", EnvBase: "OPENCODE_GO_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newOpenCodeGoProvider(apiKey, apiBase, modelType, modelName, maxTokens)
		},
	})
}

// OpenCodeGoProvider adapts nagobot's common message types to its dedicated
// gateway SDK. All model policy, wire formats and HTTP/SSE handling belong to
// internal/opencodego, independently of the other providers.
type OpenCodeGoProvider struct {
	client            *opencodego.Client
	modelType         string
	modelName         string
	effort            string
	maxTokens         int
	fallbackSessionID string
}

func newOpenCodeGoProvider(apiKey, apiBase, modelType, modelName string, maxTokens int) *OpenCodeGoProvider {
	if modelName == "" {
		modelName = modelType
	}
	modelName, _ = openCodeGoModelEffort(modelName)
	_, effort := openCodeGoModelEffort(modelType)
	return &OpenCodeGoProvider{
		client:    opencodego.NewClient(apiKey, apiBase),
		modelType: modelType, modelName: modelName, effort: effort, maxTokens: maxTokens,
		fallbackSessionID: openCodeGoRandomID(),
	}
}

func (p *OpenCodeGoProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	request, err := p.sdkRequest(req)
	if err != nil {
		return nil, fmt.Errorf("opencode-go request conversion failed: %w", err)
	}
	sessionKey := SessionKeyFromContext(ctx)
	sessionID := p.fallbackSessionID
	if sessionKey != "" {
		sum := sha256.Sum256([]byte(sessionKey))
		sessionID = hex.EncodeToString(sum[:16])
	}
	requestID := openCodeGoRandomID()
	start := time.Now()
	logger.Info("opencode-go request", "provider", "opencode-go", "modelType", p.modelType,
		"modelName", p.modelName, "reasoningEffort", p.effort, "toolCount", len(req.Tools),
		"requestID", requestID, "sessionKey", sessionKey)
	resp := &Response{ProviderLabel: "opencode-go", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)
	go func() {
		defer adapter.Finish()
		defer adapter.cancel()
		result, err := p.client.Chat(adapter.ctx, sessionID, request, func(delta opencodego.Delta) {
			switch delta.Type {
			case "text":
				adapter.EmitText(delta.Text)
			case "reasoning":
				adapter.EmitReasoning(delta.Text)
			case "tool_call":
				adapter.EmitToolCall(delta.ToolName)
			}
		})
		if err != nil {
			logger.Warn("opencode-go stream failed", "requestID", requestID, "sessionKey", sessionKey,
				"modelName", p.modelName, "latencyMs", time.Since(start).Milliseconds(), "err", err)
			adapter.SetError(fmt.Errorf("opencode-go request failed: %w", err))
			return
		}
		resp.Content = result.Content
		resp.ReasoningContent = result.ReasoningContent
		resp.ReasoningDetails = result.ReasoningDetails
		for _, call := range result.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: call.ID, Type: "function",
				Function: FunctionCall{Name: call.Name, Arguments: call.Arguments}})
		}
		resp.Usage = Usage{
			PromptTokens: result.Usage.PromptTokens, CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens: result.Usage.TotalTokens, CachedTokens: result.Usage.CachedTokens,
			ReasoningTokens: result.Usage.ReasoningTokens, CacheWriteTokens: result.Usage.CacheWriteTokens,
		}
		logger.Info("opencode-go response", "provider", "opencode-go", "modelName", p.modelName,
			"requestID", requestID, "sessionKey", sessionKey, "finishReason", result.FinishReason,
			"toolCallCount", len(resp.ToolCalls), "promptTokens", resp.Usage.PromptTokens,
			"completionTokens", resp.Usage.CompletionTokens, "cachedTokens", resp.Usage.CachedTokens,
			"cacheWriteTokens", resp.Usage.CacheWriteTokens, "reasoningTokens", resp.Usage.ReasoningTokens,
			"latencyMs", time.Since(start).Milliseconds())
	}()
	return adapter.Result(), nil
}

func (p *OpenCodeGoProvider) sdkRequest(req *Request) (opencodego.Request, error) {
	out := opencodego.Request{Model: p.modelName, Effort: p.effort, MaxTokens: p.maxTokens}
	model, _ := opencodego.ModelInfo(p.modelName)
	for _, message := range req.Messages {
		m := opencodego.Message{
			Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID,
			ReasoningContent: message.ReasoningContent, ReasoningDetails: message.ReasoningDetails,
		}
		if message.ReasoningTrimmed {
			m.ReasoningContent, m.ReasoningDetails = "", nil
		}
		var markers []MediaMarker
		if message.Role == "user" || message.Role == "tool" {
			cleaned, parsed := ParseMediaMarkers(m.Content)
			markers = parsed
			if len(parsed) > 0 {
				m.Content = cleaned
			}
		}
		_, attached := ParseMediaMarkers(strings.Join(message.Media, "\n"))
		markers = append(markers, attached...)
		if model.Vision {
			seen := map[MediaMarker]bool{}
			for _, marker := range markers {
				if seen[marker] || !strings.HasPrefix(marker.MimeType, "image/") {
					continue
				}
				seen[marker] = true
				data, err := ReadFileAsBase64(marker.FilePath)
				if err != nil {
					continue
				}
				m.Images = append(m.Images, opencodego.Image{MIMEType: marker.MimeType, Data: data})
			}
		}
		for _, call := range message.ToolCalls {
			if call.Type != "" && call.Type != "function" {
				return out, fmt.Errorf("unsupported tool call type %q", call.Type)
			}
			m.ToolCalls = append(m.ToolCalls, opencodego.ToolCall{
				ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments,
			})
		}
		out.Messages = append(out.Messages, m)
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, opencodego.Tool{
			Name: tool.Function.Name, Description: tool.Function.Description, Parameters: tool.Function.Parameters,
		})
	}
	return out, nil
}

func openCodeGoModelEffort(name string) (string, string) {
	if open := strings.IndexByte(name, '['); open >= 0 && strings.HasSuffix(name, "]") {
		return name[:open], name[open+1 : len(name)-1]
	}
	return name, ""
}

func openCodeGoRandomID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return hex.EncodeToString(id[:])
}
