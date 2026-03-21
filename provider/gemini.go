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

const geminiAPIBase = "https://generativelanguage.googleapis.com/v1beta"

func init() {
	RegisterProvider("gemini", ProviderRegistration{
		Models:       []string{"gemini-3-flash-preview"},
		VisionModels: []string{"gemini-3-flash-preview"},
		AudioModels:  []string{"gemini-3-flash-preview"},
		ContextWindows: map[string]int{
			"gemini-3-flash-preview": 1048576,
		},
		EnvKey:  "GEMINI_API_KEY",
		EnvBase: "GEMINI_API_BASE",
		Constructor: func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
			return newGeminiProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
		},
	})
}

// ---------- JSON wire types ----------

type gmRequest struct {
	SystemInstruction *gmContent         `json:"systemInstruction,omitempty"`
	Contents          []gmContent        `json:"contents"`
	GenerationConfig  gmGenerationConfig `json:"generationConfig"`
	Tools             []gmToolGroup      `json:"tools,omitempty"`
}

type gmContent struct {
	Role  string   `json:"role,omitempty"`
	Parts []gmPart `json:"parts"`
}

type gmPart struct {
	Text             string      `json:"text,omitempty"`
	Thought          *bool       `json:"thought,omitempty"`
	FunctionCall     *gmFuncCall `json:"functionCall,omitempty"`
	FunctionResponse *gmFuncResp `json:"functionResponse,omitempty"`
	InlineData       *gmBlob     `json:"inlineData,omitempty"`
	ThoughtSignature string      `json:"thoughtSignature,omitempty"`
}

type gmFuncCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type gmFuncResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gmBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gmGenerationConfig struct {
	Temperature     *float64       `json:"temperature,omitempty"`
	MaxOutputTokens int            `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *gmThinkingCfg `json:"thinkingConfig,omitempty"`
}

type gmThinkingCfg struct {
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

type gmToolGroup struct {
	FunctionDeclarations []gmFuncDecl `json:"functionDeclarations"`
}

type gmFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type gmResponse struct {
	Candidates    []gmCandidate    `json:"candidates"`
	UsageMetadata *gmUsageMetadata `json:"usageMetadata"`
	Error         *gmAPIError      `json:"error,omitempty"`
}

type gmCandidate struct {
	Content      gmContent `json:"content"`
	FinishReason string    `json:"finishReason"`
}

type gmUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
}

type gmAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ---------- provider ----------

// GeminiProvider implements the Provider interface for Google AI Studio.
type GeminiProvider struct {
	apiKey      string
	apiBase     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	client      *http.Client
}

func newGeminiProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *GeminiProvider {
	if modelName == "" {
		modelName = modelType
	}
	if apiBase == "" {
		apiBase = geminiAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")

	return &GeminiProvider{
		apiKey:      apiKey,
		apiBase:     apiBase,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		client:      &http.Client{},
	}
}

func (p *GeminiProvider) syncEndpoint() string {
	return p.apiBase + "/models/" + p.modelName + ":generateContent"
}

func (p *GeminiProvider) streamEndpoint() string {
	return p.apiBase + "/models/" + p.modelName + ":streamGenerateContent?alt=sse"
}

// Chat sends a chat completion request to Google AI Studio.
func (p *GeminiProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)
	streaming := req.OnTextDelta != nil

	logger.Info(
		"gemini request",
		"provider", "gemini",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"streaming", streaming,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	sysInstruction, contents, err := toGeminiContents(req.Messages, SupportsVision("gemini", p.modelType), SupportsAudio("gemini", p.modelType))
	if err != nil {
		return nil, fmt.Errorf("convert messages: %w", err)
	}

	gmReq := p.buildRequest(sysInstruction, contents, req.Tools)

	if streaming {
		return p.chatStream(ctx, req, gmReq, start)
	}
	return p.chatSync(ctx, gmReq, start)
}

func (p *GeminiProvider) buildRequest(sysInstruction *gmContent, contents []gmContent, tools []ToolDef) gmRequest {
	maxTokens := p.maxTokens
	if maxTokens < 16384 {
		maxTokens = 16384
	}

	r := gmRequest{
		SystemInstruction: sysInstruction,
		Contents:          contents,
		GenerationConfig: gmGenerationConfig{
			MaxOutputTokens: maxTokens,
			ThinkingConfig: &gmThinkingCfg{
				ThinkingLevel:   "high",
				IncludeThoughts: true,
			},
		},
		Tools: toGeminiTools(tools),
	}

	temp := 1.0
	r.GenerationConfig.Temperature = &temp

	return r
}

func (p *GeminiProvider) doPost(ctx context.Context, url string, body any) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)
	return p.client.Do(httpReq)
}

// chatSync handles non-streaming completion.
func (p *GeminiProvider) chatSync(ctx context.Context, gmReq gmRequest, start time.Time) (*Response, error) {
	httpResp, err := p.doPost(ctx, p.syncEndpoint(), gmReq)
	if err != nil {
		logger.Error("gemini request error", "provider", "gemini", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		var apiResp gmResponse
		if json.Unmarshal(body, &apiResp) == nil && apiResp.Error != nil {
			return nil, fmt.Errorf("gemini API error (%d): %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("gemini API error (%d): %s", httpResp.StatusCode, string(body))
	}

	var resp gmResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return p.parseResponse(resp, start)
}

// chatStream handles streaming completion with SSE.
func (p *GeminiProvider) chatStream(ctx context.Context, req *Request, gmReq gmRequest, start time.Time) (*Response, error) {
	httpResp, err := p.doPost(ctx, p.streamEndpoint(), gmReq)
	if err != nil {
		logger.Error("gemini streaming request error", "provider", "gemini", "err", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		var apiResp gmResponse
		if json.Unmarshal(body, &apiResp) == nil && apiResp.Error != nil {
			return nil, fmt.Errorf("gemini API error (%d): %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("gemini API error (%d): %s", httpResp.StatusCode, string(body))
	}

	var (
		content      strings.Builder
		reasoning    strings.Builder
		toolCalls    []ToolCall
		allParts     []gmPart // accumulate all parts for ReasoningDetails
		usage        gmUsageMetadata
		finishReason string
	)

	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	callIndex := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		var chunk gmResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			logger.Debug("gemini stream chunk parse skip", "err", err)
			continue
		}

		if chunk.UsageMetadata != nil {
			usage = *chunk.UsageMetadata
		}
		if len(chunk.Candidates) == 0 {
			continue
		}

		candidate := chunk.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = candidate.FinishReason
		}

		for _, part := range candidate.Content.Parts {
			allParts = append(allParts, part)

			isThought := part.Thought != nil && *part.Thought
			if part.Text != "" && (isThought || looksLikeThoughtLeak(part.Text)) {
				reasoning.WriteString(part.Text)
			} else if part.Text != "" {
				content.WriteString(part.Text)
				req.OnTextDelta(part.Text)
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, ToolCall{
					ID:   fmt.Sprintf("gemini_%s_%d", part.FunctionCall.Name, callIndex),
					Type: "function",
					Function: FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsJSON),
					},
				})
				callIndex++
			}
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Error("gemini stream read error", "err", err)
		return nil, fmt.Errorf("stream read error: %w", err)
	}

	finalContent := content.String()
	reasoningText := reasoning.String()

	// Build ReasoningDetails from accumulated parts (for thoughtSignature round-trip).
	var reasoningDetails json.RawMessage
	if sigParts := filterSignatureParts(allParts); len(sigParts) > 0 {
		if data, err := json.Marshal(sigParts); err == nil {
			reasoningDetails = data
		}
	}

	logger.Info(
		"gemini streaming response",
		"provider", "gemini",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", finishReason,
		"reasoningInResponse", usage.ThoughtsTokenCount > 0 || strings.TrimSpace(reasoningText) != "",
		"hasToolCalls", len(toolCalls) > 0,
		"toolCallCount", len(toolCalls),
		"promptTokens", usage.PromptTokenCount,
		"candidatesTokens", usage.CandidatesTokenCount,
		"thoughtsTokens", usage.ThoughtsTokenCount,
		"totalTokens", usage.TotalTokenCount,
		"outputChars", len(finalContent),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return &Response{
		Content:          finalContent,
		ReasoningContent: reasoningText,
		ReasoningDetails: reasoningDetails,
		ToolCalls:        toolCalls,
		Usage: Usage{
			PromptTokens:     usage.PromptTokenCount,
			CompletionTokens: usage.CandidatesTokenCount,
			TotalTokens:      usage.TotalTokenCount,
		},
		ProviderLabel: "gemini",
		ModelLabel:    p.modelName,
	}, nil
}

// parseResponse extracts content, reasoning, tool calls from a sync response.
func (p *GeminiProvider) parseResponse(resp gmResponse, start time.Time) (*Response, error) {
	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	candidate := resp.Candidates[0]
	var (
		textParts      []string
		reasoningParts []string
		toolCalls      []ToolCall
	)

	callIndex := 0
	for _, part := range candidate.Content.Parts {
		isThought := part.Thought != nil && *part.Thought
		if part.Text != "" && (isThought || looksLikeThoughtLeak(part.Text)) {
			reasoningParts = append(reasoningParts, part.Text)
		} else if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			toolCalls = append(toolCalls, ToolCall{
				ID:   fmt.Sprintf("gemini_%s_%d", part.FunctionCall.Name, callIndex),
				Type: "function",
				Function: FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			})
			callIndex++
		}
	}

	finalContent := strings.Join(textParts, "\n")
	reasoningText := strings.Join(reasoningParts, "\n")

	// Store all parts that contain thoughtSignature for round-tripping.
	var reasoningDetails json.RawMessage
	if sigParts := filterSignatureParts(candidate.Content.Parts); len(sigParts) > 0 {
		if data, err := json.Marshal(sigParts); err == nil {
			reasoningDetails = data
		}
	}

	usage := resp.UsageMetadata
	if usage == nil {
		usage = &gmUsageMetadata{}
	}

	logger.Info(
		"gemini response",
		"provider", "gemini",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"finishReason", candidate.FinishReason,
		"reasoningInResponse", usage.ThoughtsTokenCount > 0 || strings.TrimSpace(reasoningText) != "",
		"hasToolCalls", len(toolCalls) > 0,
		"toolCallCount", len(toolCalls),
		"promptTokens", usage.PromptTokenCount,
		"candidatesTokens", usage.CandidatesTokenCount,
		"thoughtsTokens", usage.ThoughtsTokenCount,
		"totalTokens", usage.TotalTokenCount,
		"outputChars", len(finalContent),
		"latencyMs", time.Since(start).Milliseconds(),
	)

	return &Response{
		Content:          finalContent,
		ReasoningContent: reasoningText,
		ReasoningDetails: reasoningDetails,
		ToolCalls:        toolCalls,
		Usage: Usage{
			PromptTokens:     usage.PromptTokenCount,
			CompletionTokens: usage.CandidatesTokenCount,
			TotalTokens:      usage.TotalTokenCount,
		},
		ProviderLabel: "gemini",
		ModelLabel:    p.modelName,
	}, nil
}

// ---------- message conversion ----------


// toGeminiContents converts canonical Messages to Gemini API format.
// Returns (systemInstruction, contents, error).
func toGeminiContents(messages []Message, visionCapable, audioCapable bool) (*gmContent, []gmContent, error) {
	var sysInstruction *gmContent
	var contents []gmContent
	var pendingParts []gmPart
	var pendingRole string

	flush := func() {
		if len(pendingParts) > 0 {
			contents = append(contents, gmContent{Role: pendingRole, Parts: pendingParts})
			pendingParts = nil
			pendingRole = ""
		}
	}

	for _, m := range messages {
		switch m.Role {
		case "system":
			// Accumulate system instructions.
			if sysInstruction == nil {
				sysInstruction = &gmContent{Parts: []gmPart{{Text: m.Content}}}
			} else {
				sysInstruction.Parts = append(sysInstruction.Parts, gmPart{Text: m.Content})
			}

		case "user":
			if pendingRole != "user" {
				flush()
				pendingRole = "user"
			}
			pendingParts = append(pendingParts, gmPart{Text: m.Content})

		case "assistant":
			if pendingRole != "model" {
				flush()
				pendingRole = "model"
			}
			// Restore parts from ReasoningDetails (thought signatures for round-trip).
			if sigParts := geminiSignatureParts(m); len(sigParts) > 0 {
				pendingParts = append(pendingParts, sigParts...)
			} else {
				// No stored parts: build from Content + ToolCalls.
				if contentStr := strings.TrimSpace(m.Content); contentStr != "" {
					pendingParts = append(pendingParts, gmPart{Text: contentStr})
				}
				for _, tc := range m.ToolCalls {
					args := parseGeminiFuncArgs(tc.Function.Arguments)
					pendingParts = append(pendingParts, gmPart{
						FunctionCall: &gmFuncCall{Name: tc.Function.Name, Args: args},
					})
				}
			}

		case "tool":
			if pendingRole != "user" {
				flush()
				pendingRole = "user"
			}
			cleanedText, markers := ParseMediaMarkers(m.GetContent())
			pendingParts = append(pendingParts, gmPart{
				FunctionResponse: &gmFuncResp{
					Name:     m.Name,
					Response: map[string]any{"result": cleanedText},
				},
			})
			// Append media from markers (images if vision-capable, audio if audio-capable).
			for _, marker := range markers {
				isImage := strings.HasPrefix(marker.MimeType, "image/")
				isAudio := strings.HasPrefix(marker.MimeType, "audio/")
				if (isImage && !visionCapable) || (isAudio && !audioCapable) {
					continue
				}
				if !isImage && !isAudio {
					continue
				}
				b64, err := ReadFileAsBase64(marker.FilePath)
				if err != nil {
					continue
				}
				pendingParts = append(pendingParts, gmPart{
					InlineData: &gmBlob{MimeType: marker.MimeType, Data: b64},
				})
			}

		default:
			return nil, nil, fmt.Errorf("unsupported message role: %s", m.Role)
		}
	}

	flush()
	return sysInstruction, contents, nil
}

// geminiSignatureParts reconstructs model message parts from ReasoningDetails.
// The stored format is []gmPart containing the original response parts
// (text+sig, functionCall+sig, etc.) needed for thought signature round-trip.
func geminiSignatureParts(m Message) []gmPart {
	if len(m.ReasoningDetails) == 0 {
		return nil
	}
	var stored []gmPart
	if err := json.Unmarshal(m.ReasoningDetails, &stored); err != nil || len(stored) == 0 {
		return nil
	}

	// Rebuild the model message parts:
	// - Include functionCall parts with their signatures
	// - Include text+signature parts
	// - Also add any content/tool_calls from the canonical message
	//   that aren't already represented in stored parts.
	var parts []gmPart

	// Collect function call names from stored parts to avoid duplicates.
	storedFCNames := map[string]bool{}
	for _, sp := range stored {
		if sp.FunctionCall != nil {
			storedFCNames[sp.FunctionCall.Name] = true
		}
	}

	// Add stored parts (these have signatures).
	for _, sp := range stored {
		// Skip thought parts (they're informational, not sent back).
		if sp.Thought != nil && *sp.Thought {
			continue
		}
		// Skip parts with no data field (e.g. from non-Gemini ReasoningDetails).
		if sp.Text == "" && sp.FunctionCall == nil && sp.FunctionResponse == nil && sp.InlineData == nil {
			continue
		}
		parts = append(parts, sp)
	}

	// Add any tool calls from the canonical message not already in stored parts.
	for _, tc := range m.ToolCalls {
		if storedFCNames[tc.Function.Name] {
			continue
		}
		args := parseGeminiFuncArgs(tc.Function.Arguments)
		parts = append(parts, gmPart{
			FunctionCall: &gmFuncCall{Name: tc.Function.Name, Args: args},
		})
	}

	// If stored parts didn't include a text part but canonical message has content,
	// add it (unless it's already there from stored parts).
	hasText := false
	for _, p := range parts {
		if p.Text != "" && p.FunctionCall == nil && p.FunctionResponse == nil {
			hasText = true
			break
		}
	}
	if !hasText && strings.TrimSpace(m.Content) != "" {
		// Prepend text before function calls.
		parts = append([]gmPart{{Text: strings.TrimSpace(m.Content)}}, parts...)
	}

	if len(parts) == 0 {
		return nil
	}
	return parts
}

// filterSignatureParts returns only the parts that carry a ThoughtSignature
// or a FunctionCall (which needs its signature for round-trip).
// Thought parts (thought:true) are excluded as they are informational only.
// looksLikeThoughtLeak detects Gemini API thinking text that leaked into
// regular content parts (without the thought:true flag). Known leak formats:
//   - ":thought\n..." prefix
//   - "_thought ..." prefix (e.g. "_thought CRITICAL INSTRUCTION")
//   - "<thought>" or "<thought ..." XML-like prefix
func looksLikeThoughtLeak(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, ":thought") {
		return true
	}
	if strings.HasPrefix(t, "_thought") {
		return true
	}
	if strings.HasPrefix(t, "<thought") {
		return true
	}
	return false
}

func filterSignatureParts(parts []gmPart) []gmPart {
	var result []gmPart
	for _, p := range parts {
		if p.Thought != nil && *p.Thought {
			continue
		}
		if p.ThoughtSignature != "" || p.FunctionCall != nil {
			result = append(result, p)
		}
		// Also keep text parts that have a signature.
		if p.Text != "" && p.ThoughtSignature != "" {
			// Already added above if sig != "".
			continue
		}
	}
	return result
}

func parseGeminiFuncArgs(arguments string) map[string]any {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		return map[string]any{"raw": trimmed}
	}
	return result
}

// ---------- tool conversion ----------

// unsupportedSchemaKeys lists JSON Schema keys that Gemini API rejects.
var unsupportedSchemaKeys = map[string]bool{
	"additionalProperties": true,
	"$schema":              true,
	"examples":             true,
}

func toGeminiTools(tools []ToolDef) []gmToolGroup {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]gmFuncDecl, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, gmFuncDecl{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  cleanGeminiSchema(t.Function.Parameters),
		})
	}
	return []gmToolGroup{{FunctionDeclarations: decls}}
}

// cleanGeminiSchema recursively removes keys that the Gemini API rejects.
func cleanGeminiSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any, len(schema))
	for k, v := range schema {
		if unsupportedSchemaKeys[k] {
			continue
		}
		switch k {
		case "properties":
			if props, ok := v.(map[string]any); ok {
				cleaned := make(map[string]any, len(props))
				for pk, pv := range props {
					if pm, ok := pv.(map[string]any); ok {
						cleaned[pk] = cleanGeminiSchema(pm)
					} else {
						cleaned[pk] = pv
					}
				}
				result[k] = cleaned
				continue
			}
		case "items":
			if m, ok := v.(map[string]any); ok {
				result[k] = cleanGeminiSchema(m)
				continue
			}
		}
		if m, ok := v.(map[string]any); ok {
			result[k] = cleanGeminiSchema(m)
			continue
		}
		result[k] = v
	}
	return result
}
