// Minimal experiment for MiniMax M2.5 API.
// Tests: basic chat, reasoning mode, tool calling with reasoning multi-turn.
//
// Usage:
//   export MINIMAX_API_KEY="your-key"
//   go run ./experiments/minimax
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

// ChatRequest uses json.RawMessage for extra fields so we can inject
// reasoning_split as a top-level bool without a dedicated struct field
// conflicting with omitempty semantics. We build it manually in chat().
type ChatRequest struct {
	Model          string    `json:"model"`
	Messages       []Message `json:"messages"`
	Tools          []Tool    `json:"tools,omitempty"`
	ToolChoice     string    `json:"tool_choice,omitempty"`
	MaxTokens      int       `json:"max_tokens,omitempty"`
	Temperature    float64   `json:"temperature,omitempty"`
	Stream         bool      `json:"stream"`
	ReasoningSplit bool      `json:"-"` // handled manually in chat()
}

type Message struct {
	Role             string            `json:"role"`
	Content          any               `json:"content"`                        // string or null
	ReasoningDetails []ReasoningDetail `json:"reasoning_details,omitempty"`    // MiniMax reasoning output (array)
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`         // for role=tool
}

type ReasoningDetail struct {
	Text string `json:"text"`
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
	Index        int         `json:"index"`
	Message      ResponseMsg `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ResponseMsg struct {
	Role             string            `json:"role"`
	Content          *string           `json:"content"`
	ReasoningDetails []ReasoningDetail `json:"reasoning_details"`
	ToolCalls        []ToolCall        `json:"tool_calls"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- client ---

const apiEndpoint = "https://api.minimaxi.com/v1/chat/completions"

var (
	apiKey string
	client = &http.Client{Timeout: 120 * time.Second}
)

func chat(req *ChatRequest) (*ChatResponse, error) {
	// Build request body manually to inject reasoning_split as top-level field.
	body, _ := json.Marshal(req)
	if req.ReasoningSplit {
		var m map[string]any
		json.Unmarshal(body, &m)
		m["reasoning_split"] = true
		body, _ = json.Marshal(m)
	}

	fmt.Printf("\n>>> REQUEST (%d bytes)\n", len(body))
	printJSON(body)

	httpReq, err := http.NewRequest("POST", apiEndpoint, bytes.NewReader(body))
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

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func formatReasoningDetails(details []ReasoningDetail) string {
	if len(details) == 0 {
		return "<none>"
	}
	var parts []string
	for i, d := range details {
		parts = append(parts, fmt.Sprintf("[%d] %s", i, d.Text))
	}
	return strings.Join(parts, "\n")
}

// --- experiments ---

func testBasicChat() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("TEST 1: Basic chat (no reasoning_split)")
	fmt.Println(strings.Repeat("=", 60))

	resp, err := chat(&ChatRequest{
		Model: "MiniMax-M2.5",
		Messages: []Message{
			{Role: "user", Content: "1+1等于几？请简短回答"},
		},
		MaxTokens:   256,
		Temperature: 1.0,
		Stream:      false,
	})
	if err != nil {
		return err
	}

	if len(resp.Choices) == 0 {
		return fmt.Errorf("no choices in response")
	}

	msg := resp.Choices[0].Message
	content := deref(msg.Content)
	if content == "<nil>" || content == "" {
		return fmt.Errorf("empty content in response")
	}

	fmt.Printf("\n--- RESULT ---\n")
	fmt.Printf("Content: %s\n", content)
	fmt.Printf("FinishReason: %s\n", resp.Choices[0].FinishReason)
	fmt.Printf("Usage: %d prompt + %d completion = %d total\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	return nil
}

func testReasoning() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("TEST 2: Reasoning mode (reasoning_split: true)")
	fmt.Println(strings.Repeat("=", 60))

	resp, err := chat(&ChatRequest{
		Model: "MiniMax-M2.5",
		Messages: []Message{
			{Role: "user", Content: "如果一个人每天存10元，第一天存10元，第二天存20元，第三天存30元...第100天存多少？总共存了多少？"},
		},
		MaxTokens:      4096,
		Temperature:    1.0,
		Stream:         false,
		ReasoningSplit:  true,
	})
	if err != nil {
		return err
	}

	if len(resp.Choices) == 0 {
		return fmt.Errorf("no choices in response")
	}

	msg := resp.Choices[0].Message

	fmt.Printf("\n--- RESULT ---\n")
	fmt.Printf("ReasoningDetails (%d parts):\n%s\n", len(msg.ReasoningDetails), formatReasoningDetails(msg.ReasoningDetails))
	fmt.Printf("Content: %s\n", deref(msg.Content))
	fmt.Printf("FinishReason: %s\n", resp.Choices[0].FinishReason)
	return nil
}

func testToolCallingWithReasoning() error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("TEST 3: Tool calling + reasoning multi-turn")
	fmt.Println(strings.Repeat("=", 60))

	tools := []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_current_time",
			Description: "Get current time for a given timezone",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timezone": map[string]any{
						"type":        "string",
						"description": "IANA timezone name, e.g. Asia/Shanghai",
					},
				},
				"required": []string{"timezone"},
			},
		},
	}}

	messages := []Message{
		{Role: "user", Content: "北京现在几点？"},
	}

	// Round 1: expect model to call get_current_time
	fmt.Println("\n--- Round 1: user -> model (expect tool_calls) ---")
	resp, err := chat(&ChatRequest{
		Model:          "MiniMax-M2.5",
		Messages:       messages,
		Tools:          tools,
		ToolChoice:     "auto",
		MaxTokens:      4096,
		Temperature:    1.0,
		Stream:         false,
		ReasoningSplit: true,
	})
	if err != nil {
		return err
	}

	if len(resp.Choices) == 0 {
		return fmt.Errorf("no choices in response")
	}

	msg := resp.Choices[0].Message
	fmt.Printf("\n--- Round 1 RESULT ---\n")
	fmt.Printf("FinishReason: %s\n", resp.Choices[0].FinishReason)
	fmt.Printf("ReasoningDetails (%d parts): %s\n", len(msg.ReasoningDetails), formatReasoningDetails(msg.ReasoningDetails))
	fmt.Printf("Content: %v\n", deref(msg.Content))
	fmt.Printf("ToolCalls: %d\n", len(msg.ToolCalls))

	if resp.Choices[0].FinishReason != "tool_calls" {
		return fmt.Errorf("expected finish_reason 'tool_calls', got '%s'", resp.Choices[0].FinishReason)
	}

	if len(msg.ToolCalls) == 0 {
		return fmt.Errorf("model did not call any tools")
	}

	for _, tc := range msg.ToolCalls {
		fmt.Printf("  -> %s(%s) id=%s\n", tc.Function.Name, tc.Function.Arguments, tc.ID)
	}

	// Build assistant message preserving reasoning_details for multi-turn
	assistantMsg := Message{
		Role:             "assistant",
		Content:          msg.Content, // may be nil
		ReasoningDetails: msg.ReasoningDetails,
		ToolCalls:        msg.ToolCalls,
	}
	messages = append(messages, assistantMsg)

	// Simulate tool result with current Beijing time
	now := time.Now().In(time.FixedZone("CST", 8*3600))
	toolResult := Message{
		Role:       "tool",
		ToolCallID: msg.ToolCalls[0].ID,
		Content:    fmt.Sprintf(`{"timezone":"Asia/Shanghai","current_time":"%s"}`, now.Format("2006-01-02 15:04:05")),
	}
	messages = append(messages, toolResult)

	// Round 2: model processes tool result
	fmt.Println("\n--- Round 2: tool result -> model (expect final answer) ---")
	resp2, err := chat(&ChatRequest{
		Model:          "MiniMax-M2.5",
		Messages:       messages,
		Tools:          tools,
		ToolChoice:     "auto",
		MaxTokens:      4096,
		Temperature:    1.0,
		Stream:         false,
		ReasoningSplit: true,
	})
	if err != nil {
		return err
	}

	if len(resp2.Choices) == 0 {
		return fmt.Errorf("no choices in round 2 response")
	}

	msg2 := resp2.Choices[0].Message
	fmt.Printf("\n--- Round 2 RESULT ---\n")
	fmt.Printf("FinishReason: %s\n", resp2.Choices[0].FinishReason)
	fmt.Printf("ReasoningDetails (%d parts): %s\n", len(msg2.ReasoningDetails), formatReasoningDetails(msg2.ReasoningDetails))
	fmt.Printf("Content: %s\n", deref(msg2.Content))
	fmt.Printf("ToolCalls: %d\n", len(msg2.ToolCalls))

	content := deref(msg2.Content)
	if content == "<nil>" || content == "" {
		return fmt.Errorf("empty final content after tool result")
	}
	return nil
}

func main() {
	apiKey = os.Getenv("MINIMAX_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "MINIMAX_API_KEY is required")
		os.Exit(1)
	}

	fmt.Printf("Endpoint: %s\n", apiEndpoint)
	fmt.Printf("API Key: ****%s\n", apiKey[max(0, len(apiKey)-4):])

	tests := []struct {
		name string
		fn   func() error
	}{
		{"BasicChat", testBasicChat},
		{"Reasoning", testReasoning},
		{"ToolCallingWithReasoning", testToolCallingWithReasoning},
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
