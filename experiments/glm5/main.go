// Minimal experiment for Zhipu GLM-5 API.
// Tests: basic chat, thinking mode, tool calling with reasoning_content round-trip.
//
// Usage:
//   export GLM_API_KEY="your-key"
//   export GLM_BASE_URL="https://open.bigmodel.cn/api/paas/v4"  # or https://api.z.ai/api/paas/v4
//   go run ./experiments/glm5
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// --- request types ---

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
	Thinking    *Thinking `json:"thinking,omitempty"`
}

type Thinking struct {
	Type          string `json:"type"`                      // "enabled" or "disabled"
	ClearThinking *bool  `json:"clear_thinking,omitempty"` // false = preserve reasoning in context
}

type Message struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`                     // string or null
	ReasoningContent string     `json:"reasoning_content,omitempty"` // GLM thinking output
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"` // for role=tool
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// --- response types ---

type ChatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Index        int            `json:"index"`
	Message      ResponseMsg    `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type ResponseMsg struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent *string    `json:"reasoning_content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- client ---

var (
	apiKey  string
	baseURL string
	client  = &http.Client{Timeout: 120 * time.Second}
)

func chat(req *ChatRequest) (*ChatResponse, error) {
	body, _ := json.Marshal(req)

	fmt.Printf("\n>>> REQUEST (%d bytes)\n", len(body))
	printJSON(body)

	httpReq, err := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("\n<<< RESPONSE %d (%d bytes)\n", resp.StatusCode, len(respBody))
	printJSON(respBody)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	return &chatResp, nil
}

func printJSON(data []byte) {
	var buf bytes.Buffer
	if json.Indent(&buf, data, "  ", "  ") == nil {
		fmt.Println("  " + buf.String())
	} else {
		fmt.Println("  " + string(data))
	}
}

func boolPtr(b bool) *bool { return &b }

// --- experiments ---

func testBasicChat() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("TEST 1: Basic chat (no thinking)")
	fmt.Println(strings.Repeat("=", 60))

	resp, err := chat(&ChatRequest{
		Model: "glm-5",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant. Reply concisely."},
			{Role: "user", Content: "What is 2+3? One word answer."},
		},
		MaxTokens:   256,
		Temperature: 0.7,
		Stream:      false,
		Thinking:    &Thinking{Type: "disabled"},
	})
	if err != nil {
		return err
	}

	msg := resp.Choices[0].Message
	fmt.Printf("\n--- RESULT ---\n")
	fmt.Printf("Content: %v\n", deref(msg.Content))
	fmt.Printf("ReasoningContent: %v\n", deref(msg.ReasoningContent))
	fmt.Printf("FinishReason: %s\n", resp.Choices[0].FinishReason)
	fmt.Printf("Usage: %d prompt + %d completion = %d total\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	return nil
}

func testThinking() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("TEST 2: Thinking mode enabled")
	fmt.Println(strings.Repeat("=", 60))

	resp, err := chat(&ChatRequest{
		Model: "glm-5",
		Messages: []Message{
			{Role: "user", Content: "How many r's are in the word 'strawberry'? Think step by step."},
		},
		MaxTokens:   4096,
		Temperature: 1.0,
		Stream:      false,
		Thinking:    &Thinking{Type: "enabled"},
	})
	if err != nil {
		return err
	}

	msg := resp.Choices[0].Message
	fmt.Printf("\n--- RESULT ---\n")
	fmt.Printf("ReasoningContent (%d chars): %.200s...\n", len(deref(msg.ReasoningContent)), deref(msg.ReasoningContent))
	fmt.Printf("Content: %s\n", deref(msg.Content))
	fmt.Printf("FinishReason: %s\n", resp.Choices[0].FinishReason)
	return nil
}

func testToolCallingWithThinking() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("TEST 3: Tool calling + thinking (multi-turn round-trip)")
	fmt.Println(strings.Repeat("=", 60))

	tools := []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_weather",
			Description: "Get current weather for a city",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{
						"type":        "string",
						"description": "City name, e.g. Beijing",
					},
				},
				"required": []string{"city"},
			},
		},
	}}

	messages := []Message{
		{Role: "user", Content: "What's the weather in Beijing? Use the tool."},
	}

	thinking := &Thinking{Type: "enabled", ClearThinking: boolPtr(false)}

	// Round 1: expect model to call get_weather
	fmt.Println("\n--- Round 1: user → model (expect tool_calls) ---")
	resp, err := chat(&ChatRequest{
		Model:       "glm-5",
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  "auto",
		MaxTokens:   4096,
		Temperature: 1.0,
		Stream:      false,
		Thinking:    thinking,
	})
	if err != nil {
		return err
	}

	msg := resp.Choices[0].Message
	fmt.Printf("\n--- Round 1 RESULT ---\n")
	fmt.Printf("FinishReason: %s\n", resp.Choices[0].FinishReason)
	fmt.Printf("ReasoningContent: %.200s\n", deref(msg.ReasoningContent))
	fmt.Printf("Content: %v\n", deref(msg.Content))
	fmt.Printf("ToolCalls: %d\n", len(msg.ToolCalls))

	if len(msg.ToolCalls) == 0 {
		fmt.Println("WARN: model did not call any tools. Stopping.")
		return nil
	}

	for _, tc := range msg.ToolCalls {
		fmt.Printf("  -> %s(%s) id=%s\n", tc.Function.Name, tc.Function.Arguments, tc.ID)
	}

	// Build assistant message preserving reasoning_content
	assistantMsg := Message{
		Role:             "assistant",
		Content:          msg.Content, // may be nil
		ReasoningContent: deref(msg.ReasoningContent),
		ToolCalls:        msg.ToolCalls,
	}
	messages = append(messages, assistantMsg)

	// Add tool result
	toolResult := Message{
		Role:       "tool",
		ToolCallID: msg.ToolCalls[0].ID,
		Content:    `{"city":"Beijing","weather":"Sunny","temperature":"28°C","humidity":"45%"}`,
	}
	messages = append(messages, toolResult)

	// Round 2: model processes tool result
	fmt.Println("\n--- Round 2: tool result → model (expect final answer) ---")
	resp2, err := chat(&ChatRequest{
		Model:       "glm-5",
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  "auto",
		MaxTokens:   4096,
		Temperature: 1.0,
		Stream:      false,
		Thinking:    thinking,
	})
	if err != nil {
		return err
	}

	msg2 := resp2.Choices[0].Message
	fmt.Printf("\n--- Round 2 RESULT ---\n")
	fmt.Printf("FinishReason: %s\n", resp2.Choices[0].FinishReason)
	fmt.Printf("ReasoningContent: %.200s\n", deref(msg2.ReasoningContent))
	fmt.Printf("Content: %s\n", deref(msg2.Content))
	fmt.Printf("ToolCalls: %d\n", len(msg2.ToolCalls))
	return nil
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func main() {
	apiKey = os.Getenv("GLM_API_KEY")
	baseURL = strings.TrimRight(os.Getenv("GLM_BASE_URL"), "/")

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "GLM_API_KEY is required")
		os.Exit(1)
	}
	if baseURL == "" {
		baseURL = "https://open.bigmodel.cn/api/paas/v4"
		fmt.Printf("GLM_BASE_URL not set, using default: %s\n", baseURL)
	}

	fmt.Printf("Base URL: %s\n", baseURL)
	fmt.Printf("API Key: ****%s\n", apiKey[max(0, len(apiKey)-4):])

	tests := []struct {
		name string
		fn   func() error
	}{
		{"BasicChat", testBasicChat},
		{"Thinking", testThinking},
		{"ToolCallingWithThinking", testToolCallingWithThinking},
	}

	for _, t := range tests {
		if err := t.fn(); err != nil {
			fmt.Fprintf(os.Stderr, "\nFAIL %s: %v\n", t.name, err)
			os.Exit(1)
		}
		fmt.Printf("\nPASS %s\n", t.name)
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("ALL TESTS PASSED")
}
