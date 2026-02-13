package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/openai/openai-go/v3"
	oaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/tidwall/sjson"
)

// TestExtractMinimaxReasoning verifies the reasoning extraction from raw JSON.
func TestExtractMinimaxReasoning(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{
			name:     "empty",
			raw:      "",
			expected: "",
		},
		{
			name:     "no reasoning_details",
			raw:      `{"role":"assistant","content":"hello"}`,
			expected: "",
		},
		{
			name:     "empty reasoning_details array",
			raw:      `{"role":"assistant","content":"hello","reasoning_details":[]}`,
			expected: "",
		},
		{
			name:     "single reasoning detail",
			raw:      `{"role":"assistant","content":"hello","reasoning_details":[{"text":"I need to think about this"}]}`,
			expected: "I need to think about this",
		},
		{
			name:     "multiple reasoning details",
			raw:      `{"role":"assistant","content":"hello","reasoning_details":[{"text":"Step 1"},{"text":"Step 2"}]}`,
			expected: "Step 1\nStep 2",
		},
		{
			name:     "reasoning details with whitespace-only text",
			raw:      `{"role":"assistant","content":"hello","reasoning_details":[{"text":"  "},{"text":"real text"}]}`,
			expected: "real text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMinimaxReasoning(tt.raw)
			if got != tt.expected {
				t.Errorf("extractMinimaxReasoning() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestSjsonReasoningSplitTopLevel directly tests that tidwall/sjson places
// "reasoning_split" at the top level when the key has no dots.
func TestSjsonReasoningSplitTopLevel(t *testing.T) {
	baseJSON := `{"model":"MiniMax-M2.5","messages":[{"role":"user","content":"hello"}],"temperature":1}`

	result, err := sjson.SetBytes([]byte(baseJSON), "reasoning_split", true)
	if err != nil {
		t.Fatalf("sjson.SetBytes failed: %v", err)
	}

	t.Logf("Result JSON: %s", string(result))

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// Verify reasoning_split is at top level
	val, ok := parsed["reasoning_split"]
	if !ok {
		t.Fatal("reasoning_split not found at top level")
	}
	if val != true {
		t.Errorf("reasoning_split = %v, want true", val)
	}

	// Verify it is NOT nested
	if _, ok := parsed["extra_body"]; ok {
		t.Error("unexpected extra_body key found")
	}
}

// TestMinimaxSDKRequestBody intercepts the actual HTTP request sent by the
// openai-go SDK to verify that reasoning_split is present at the top level.
func TestMinimaxSDKRequestBody(t *testing.T) {
	var capturedBody map[string]any

	// Create a test HTTP server that captures the request body and returns
	// a valid chat completion response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
			w.WriteHeader(500)
			return
		}
		t.Logf("Captured request body: %s", string(body))

		if err := json.Unmarshal(body, &capturedBody); err != nil {
			t.Errorf("failed to parse request body: %v", err)
			w.WriteHeader(500)
			return
		}

		// Return a mock response with reasoning_details
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "test-id",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "MiniMax-M2.5",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "The answer is 2.",
						"reasoning_details": []map[string]any{
							{"text": "The user asks 1+1. Let me think..."},
							{"text": "1+1 = 2."},
						},
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 30,
				},
			},
		})
	}))
	defer server.Close()

	// Create a client pointing to the test server
	client := openai.NewClient(
		oaioption.WithAPIKey("test-key"),
		oaioption.WithBaseURL(server.URL),
	)

	chatReq := openai.ChatCompletionNewParams{
		Model: shared.ChatModel("MiniMax-M2.5"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("What is 1+1?"),
		},
		Temperature: openai.Float(1.0),
		MaxTokens:   openai.Int(4096),
	}

	requestOpts := []oaioption.RequestOption{
		oaioption.WithJSONSet("reasoning_split", true),
	}

	resp, err := client.Chat.Completions.New(context.Background(), chatReq, requestOpts...)
	if err != nil {
		t.Fatalf("SDK request failed: %v", err)
	}

	// Verify the request body has reasoning_split at the top level
	val, ok := capturedBody["reasoning_split"]
	if !ok {
		t.Fatal("BUG: reasoning_split NOT found in the request body sent to server")
	}
	if val != true {
		t.Errorf("reasoning_split = %v, want true", val)
	}

	// Verify it's NOT nested under extra_body
	if _, ok := capturedBody["extra_body"]; ok {
		t.Error("BUG: reasoning_split was nested under extra_body instead of being top-level")
	}

	// Verify other expected fields
	if capturedBody["model"] != "MiniMax-M2.5" {
		t.Errorf("model = %v, want MiniMax-M2.5", capturedBody["model"])
	}

	t.Logf("Verified: reasoning_split=true is at the TOP LEVEL of the request body")

	// Now verify the response parsing: does RawJSON() preserve reasoning_details?
	choice := resp.Choices[0]
	rawMsg := choice.Message.RawJSON()
	t.Logf("Response RawJSON: %s", rawMsg)

	reasoningText := extractMinimaxReasoning(rawMsg)
	t.Logf("Extracted reasoning: %q", reasoningText)

	if reasoningText == "" {
		t.Error("BUG: extractMinimaxReasoning returned empty string from response with reasoning_details")
	}

	expectedReasoning := "The user asks 1+1. Let me think...\n1+1 = 2."
	if reasoningText != expectedReasoning {
		t.Errorf("reasoning = %q, want %q", reasoningText, expectedReasoning)
	}

	// Verify reasoning tokens are parsed
	reasoningTokens := resp.Usage.CompletionTokensDetails.ReasoningTokens
	if reasoningTokens != 30 {
		t.Errorf("reasoningTokens = %d, want 30", reasoningTokens)
	}

	t.Log("All checks passed: SDK correctly sends reasoning_split and parses reasoning_details")
}
