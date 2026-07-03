package provider

import (
	"testing"
)

func newTestOAuthProvider() *OpenAIProvider {
	p := newOpenAIProvider("test-key", "", "gpt-5.5", "gpt-5.5", 0, 0)
	p.SetAccountID("acct-123")
	return p
}

// TestBuildRequestBody_FirstCallIsFullContext verifies a fresh provider
// instance (no prior response) always sends full context with no
// previous_response_id, even on the OAuth backend.
func TestBuildRequestBody_FirstCallIsFullContext(t *testing.T) {
	p := newTestOAuthProvider()
	req := &Request{Messages: []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}}

	built, err := p.buildRequestBody(req, false)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	if built.usedDelta {
		t.Fatalf("expected first call to not use delta")
	}
	body := built.bodyMap
	if _, ok := body["previous_response_id"]; ok {
		t.Fatalf("expected no previous_response_id on first call")
	}
	input, _ := body["input"].([]map[string]any)
	if len(input) != 1 {
		t.Fatalf("expected 1 input item (user message only), got %d", len(input))
	}
}

// TestBuildRequestBody_DeltaOnMatchingPrefix verifies that once continuation
// state is recorded, a subsequent call whose message history extends that
// state (same prefix + new tool result) sends only the new item plus
// previous_response_id.
func TestBuildRequestBody_DeltaOnMatchingPrefix(t *testing.T) {
	p := newTestOAuthProvider()
	req1 := &Request{Messages: []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "call a tool"},
	}}
	built1, err := p.buildRequestBody(req1, false)
	if err != nil {
		t.Fatalf("buildRequestBody (1): %v", err)
	}

	// Simulate a successful response with one tool call.
	resp := &Response{
		Content: "",
		ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: FunctionCall{Name: "search", Arguments: "{}"}},
		},
	}
	p.updateContinuation(built1.fullItems, "resp_abc", resp)

	if p.lastResponseID != "resp_abc" {
		t.Fatalf("expected lastResponseID to be recorded, got %q", p.lastResponseID)
	}

	// Next iteration: history now includes the assistant's tool call and its result.
	req2 := &Request{Messages: []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "call a tool"},
		AssistantMessageWithTools("", "", nil, resp.ToolCalls),
		ToolResultMessage("call_1", "search", "result data"),
	}}

	built2, err := p.buildRequestBody(req2, false)
	if err != nil {
		t.Fatalf("buildRequestBody (2): %v", err)
	}
	if !built2.usedDelta {
		t.Fatalf("expected second call to use delta")
	}
	body := built2.bodyMap
	if body["previous_response_id"] != "resp_abc" {
		t.Fatalf("expected previous_response_id=resp_abc, got %v", body["previous_response_id"])
	}
	input, _ := body["input"].([]map[string]any)
	if len(input) != 1 {
		t.Fatalf("expected exactly 1 delta item (the tool result), got %d: %+v", len(input), input)
	}
	item := input[0]
	if item["type"] != "function_call_output" {
		t.Fatalf("expected delta item to be the tool result, got %+v", item)
	}
}

// TestBuildRequestBody_MismatchFallsBackToFull verifies that if the message
// history no longer has the recorded baseline as an exact prefix (e.g.
// compression rewrote earlier content), the provider safely falls back to a
// full-context request instead of sending a bogus delta.
func TestBuildRequestBody_MismatchFallsBackToFull(t *testing.T) {
	p := newTestOAuthProvider()
	req1 := &Request{Messages: []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "original message"},
	}}
	built1, err := p.buildRequestBody(req1, false)
	if err != nil {
		t.Fatalf("buildRequestBody (1): %v", err)
	}
	resp := &Response{Content: "ok"}
	p.updateContinuation(built1.fullItems, "resp_xyz", resp)

	// History was rewritten (e.g. compression) instead of purely appended to.
	req2 := &Request{Messages: []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "a DIFFERENT, compressed message"},
	}}
	built2, err := p.buildRequestBody(req2, false)
	if err != nil {
		t.Fatalf("buildRequestBody (2): %v", err)
	}
	if built2.usedDelta {
		t.Fatalf("expected fallback to full context on prefix mismatch")
	}
	body := built2.bodyMap
	if _, ok := body["previous_response_id"]; ok {
		t.Fatalf("expected no previous_response_id after mismatch fallback")
	}
}

// TestBuildRequestBody_ForceFullContextIgnoresContinuation verifies the retry
// path (forceFullContext=true) always bypasses any recorded continuation
// state, even when it would otherwise match.
func TestBuildRequestBody_ForceFullContextIgnoresContinuation(t *testing.T) {
	p := newTestOAuthProvider()
	req1 := &Request{Messages: []Message{{Role: "user", Content: "hi"}}}
	built1, _ := p.buildRequestBody(req1, false)
	p.updateContinuation(built1.fullItems, "resp_1", &Response{Content: "hello"})

	req2 := &Request{Messages: []Message{
		{Role: "user", Content: "hi"},
		AssistantMessageWithTools("hello", "", nil, nil),
		{Role: "user", Content: "follow up"},
	}}
	built2, err := p.buildRequestBody(req2, true)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	if built2.usedDelta {
		t.Fatalf("forceFullContext must never use delta")
	}
	body := built2.bodyMap
	if _, ok := body["previous_response_id"]; ok {
		t.Fatalf("forceFullContext must not send previous_response_id")
	}
}

// TestBuildRequestBody_NonOAuthNeverUsesDelta verifies the plain api.openai.com
// (API-key) path never attempts previous_response_id chaining, since that
// backend's store:false actually means the response id isn't referenceable
// (unlike the ChatGPT/Codex backend's connection-scoped continuation).
func TestBuildRequestBody_NonOAuthNeverUsesDelta(t *testing.T) {
	p := newOpenAIProvider("test-key", "", "gpt-5.5", "gpt-5.5", 0, 0) // no SetAccountID call
	req1 := &Request{Messages: []Message{{Role: "user", Content: "hi"}}}
	built1, _ := p.buildRequestBody(req1, false)
	p.updateContinuation(built1.fullItems, "resp_1", &Response{Content: "hello"})

	req2 := &Request{Messages: []Message{
		{Role: "user", Content: "hi"},
		AssistantMessageWithTools("hello", "", nil, nil),
		{Role: "user", Content: "follow up"},
	}}
	built2, err := p.buildRequestBody(req2, false)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	if built2.usedDelta {
		t.Fatalf("non-oauth provider must never use delta")
	}
}

// TestUpdateContinuation_NoResponseIDInvalidates verifies that a response
// with no id (unexpected/older API shape) invalidates rather than silently
// recording an empty previous_response_id that would corrupt the next call.
func TestUpdateContinuation_NoResponseIDInvalidates(t *testing.T) {
	p := newTestOAuthProvider()
	p.lastResponseID = "stale"
	p.lastInputItems = []map[string]any{{"type": "message"}}
	p.updateContinuation([]map[string]any{{"type": "message"}}, "", &Response{})
	if p.lastResponseID != "" || p.lastInputItems != nil {
		t.Fatalf("expected continuation to be invalidated when responseID is empty")
	}
}
