package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestToGeminiContents_BasicConversation(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
	}
	sys, contents, err := toGeminiContents(msgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if sys == nil || sys.Parts[0].Text != "You are helpful." {
		t.Error("system instruction not extracted")
	}
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}
	if contents[0].Role != "user" || contents[0].Parts[0].Text != "Hello" {
		t.Error("first user message wrong")
	}
	if contents[1].Role != "model" || contents[1].Parts[0].Text != "Hi there!" {
		t.Error("assistant not mapped to model")
	}
	if contents[2].Role != "user" {
		t.Error("second user message wrong role")
	}
}

func TestToGeminiContents_MergesConsecutiveRoles(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "user", Content: "World"},
	}
	_, contents, err := toGeminiContents(msgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 merged content, got %d", len(contents))
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(contents[0].Parts))
	}
}

func TestToGeminiContents_ToolCallRoundTrip(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: FunctionCall{
					Name:      "get_weather",
					Arguments: `{"location":"Paris"}`,
				},
			}},
		},
		{Role: "tool", Content: `{"temp":"20C"}`, Name: "get_weather", ToolCallID: "call_1"},
	}
	_, contents, err := toGeminiContents(msgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}
	// assistant -> model with functionCall
	modelMsg := contents[1]
	if modelMsg.Role != "model" {
		t.Error("assistant not mapped to model")
	}
	if len(modelMsg.Parts) != 1 || modelMsg.Parts[0].FunctionCall == nil {
		t.Error("expected functionCall part")
	}
	if modelMsg.Parts[0].FunctionCall.Name != "get_weather" {
		t.Error("wrong function name")
	}
	// tool -> user with functionResponse
	toolMsg := contents[2]
	if toolMsg.Role != "user" {
		t.Error("tool not mapped to user")
	}
	if toolMsg.Parts[0].FunctionResponse == nil {
		t.Error("expected functionResponse part")
	}
	if toolMsg.Parts[0].FunctionResponse.Name != "get_weather" {
		t.Error("wrong function response name")
	}
}

func TestToGeminiContents_ThoughtSignatureRoundTrip(t *testing.T) {
	// Simulate stored parts from a previous response with thoughtSignature.
	storedParts := []gmPart{
		{
			FunctionCall:     &gmFuncCall{Name: "get_weather", Args: map[string]any{"location": "Paris"}},
			ThoughtSignature: "test_signature_abc123",
		},
	}
	storedJSON, _ := json.Marshal(storedParts)

	msgs := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningDetails: storedJSON,
			ToolCalls: []ToolCall{{
				ID:       "gemini_get_weather_0",
				Type:     "function",
				Function: FunctionCall{Name: "get_weather", Arguments: `{"location":"Paris"}`},
			}},
		},
		{Role: "tool", Content: `{"temp":"20C"}`, Name: "get_weather", ToolCallID: "gemini_get_weather_0"},
	}
	_, contents, err := toGeminiContents(msgs, false)
	if err != nil {
		t.Fatal(err)
	}

	modelMsg := contents[1]
	if modelMsg.Role != "model" {
		t.Fatal("expected model role")
	}
	// Should have the stored parts with signature.
	foundSig := false
	for _, p := range modelMsg.Parts {
		if p.ThoughtSignature == "test_signature_abc123" {
			foundSig = true
		}
	}
	if !foundSig {
		t.Error("thoughtSignature not round-tripped")
	}
}

func TestCleanGeminiSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":                 "string",
				"description":          "City name",
				"additionalProperties": false,
			},
			"items": map[string]any{
				"type":     "array",
				"$schema":  "http://json-schema.org/draft-07/schema#",
				"examples": []string{"a", "b"},
				"items": map[string]any{
					"type":                 "string",
					"additionalProperties": true,
				},
			},
		},
		"required":             []string{"location"},
		"additionalProperties": false,
	}

	cleaned := cleanGeminiSchema(schema)

	// Top-level additionalProperties should be removed.
	if _, ok := cleaned["additionalProperties"]; ok {
		t.Error("top-level additionalProperties not removed")
	}

	// Nested additionalProperties in properties should be removed.
	props := cleaned["properties"].(map[string]any)
	loc := props["location"].(map[string]any)
	if _, ok := loc["additionalProperties"]; ok {
		t.Error("nested additionalProperties not removed")
	}
	if loc["description"] != "City name" {
		t.Error("description should be preserved")
	}

	items := props["items"].(map[string]any)
	if _, ok := items["$schema"]; ok {
		t.Error("$schema not removed")
	}
	if _, ok := items["examples"]; ok {
		t.Error("examples not removed")
	}

	// Nested items schema should also be cleaned.
	nestedItems := items["items"].(map[string]any)
	if _, ok := nestedItems["additionalProperties"]; ok {
		t.Error("deeply nested additionalProperties not removed")
	}
}

func TestFilterSignatureParts(t *testing.T) {
	tr := true
	parts := []gmPart{
		{Text: "thinking...", Thought: &tr},
		{Text: "response text", ThoughtSignature: "sig123"},
		{FunctionCall: &gmFuncCall{Name: "fn1", Args: map[string]any{}}, ThoughtSignature: "sig456"},
		{FunctionCall: &gmFuncCall{Name: "fn2", Args: map[string]any{}}},
	}

	filtered := filterSignatureParts(parts)

	// Should exclude thought part, include sig parts and functionCall parts.
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered parts, got %d", len(filtered))
	}
	// First should be text+sig.
	if filtered[0].Text != "response text" || filtered[0].ThoughtSignature != "sig123" {
		t.Error("text+sig part not preserved")
	}
	// Second should be functionCall+sig.
	if filtered[1].FunctionCall == nil || filtered[1].ThoughtSignature != "sig456" {
		t.Error("functionCall+sig part not preserved")
	}
	// Third should be functionCall without sig.
	if filtered[2].FunctionCall == nil || filtered[2].FunctionCall.Name != "fn2" {
		t.Error("functionCall without sig not preserved")
	}
}

func TestGeminiSyncResponse(t *testing.T) {
	// Mock Gemini API server.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Error("missing or wrong API key header")
		}
		resp := gmResponse{
			Candidates: []gmCandidate{{
				Content: gmContent{
					Role: "model",
					Parts: []gmPart{
						{Text: "The answer is 4.", ThoughtSignature: "sig_abc"},
					},
				},
				FinishReason: "STOP",
			}},
			UsageMetadata: &gmUsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 5,
				TotalTokenCount:      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &GeminiProvider{
		apiKey:    "test-key",
		apiBase:   server.URL,
		modelName: "test-model",
		modelType: "test-model",
		maxTokens: 1024,
		client:    &http.Client{},
	}

	result, err := p.Chat(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "What is 2+2?"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "The answer is 4." {
		t.Errorf("unexpected content: %q", result.Content)
	}
	if result.Usage.PromptTokens != 10 {
		t.Errorf("unexpected prompt tokens: %d", result.Usage.PromptTokens)
	}
	if len(result.ReasoningDetails) == 0 {
		t.Error("expected ReasoningDetails with signature")
	}
}

func TestGeminiToolCallIDSynthesis(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := gmResponse{
			Candidates: []gmCandidate{{
				Content: gmContent{
					Role: "model",
					Parts: []gmPart{
						{FunctionCall: &gmFuncCall{Name: "fn_a", Args: map[string]any{"x": "1"}}, ThoughtSignature: "sig1"},
						{FunctionCall: &gmFuncCall{Name: "fn_b", Args: map[string]any{"y": "2"}}},
					},
				},
				FinishReason: "STOP",
			}},
			UsageMetadata: &gmUsageMetadata{},
		}
		json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &GeminiProvider{
		apiKey:    "test-key",
		apiBase:   server.URL,
		modelName: "test-model",
		modelType: "test-model",
		maxTokens: 1024,
		client:    &http.Client{},
	}

	result, err := p.Chat(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "gemini_fn_a_0" {
		t.Errorf("unexpected first tool call ID: %s", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[1].ID != "gemini_fn_b_1" {
		t.Errorf("unexpected second tool call ID: %s", result.ToolCalls[1].ID)
	}
	if result.ToolCalls[0].Function.Name != "fn_a" {
		t.Error("wrong function name")
	}
}
