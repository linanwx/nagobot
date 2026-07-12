package provider

import (
	"context"
	"testing"
)

// TestCacheWriteTokensSurviveTheParse pins the one thing that made this field
// worth adding: gpt-5.6 bills cache_write_tokens at 1.25x the uncached input
// rate, so on a cold prefix it is the most expensive line of the request — and
// before this it was dropped on the floor, invisible in both the logs and the
// metrics store. (Codex itself has the same bug: openai/codex#32479.)
//
// The payload below is the real shape of a Responses API response.completed
// event. handleEvent is shared by the SSE (HTTP) and WebSocket transports, so
// covering it once covers both.
func TestCacheWriteTokensSurviveTheParse(t *testing.T) {
	resp := &Response{}
	adapter := newStreamAdapter(context.Background(), resp)
	a := newResponseAssembler(adapter)

	event := []byte(`{
		"type": "response.completed",
		"response": {
			"id": "resp_abc",
			"usage": {
				"input_tokens": 190000,
				"output_tokens": 800,
				"total_tokens": 190800,
				"input_tokens_details": {
					"cached_tokens": 40000,
					"cache_write_tokens": 150000
				},
				"output_tokens_details": {"reasoning_tokens": 300}
			}
		}
	}`)

	done, err := a.handleEvent(event)
	if err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if !done {
		t.Fatal("response.completed did not report done")
	}

	if got := resp.Usage.CacheWriteTokens; got != 150000 {
		t.Errorf("CacheWriteTokens = %d, want 150000 — the field is being dropped again", got)
	}

	// The neighbouring fields must not have been disturbed by the new one.
	if got := resp.Usage.CachedTokens; got != 40000 {
		t.Errorf("CachedTokens = %d, want 40000", got)
	}
	if got := resp.Usage.PromptTokens; got != 190000 {
		t.Errorf("PromptTokens = %d, want 190000", got)
	}
	if got := resp.Usage.ReasoningTokens; got != 300 {
		t.Errorf("ReasoningTokens = %d, want 300", got)
	}
}

// TestCacheWriteTokensAbsentIsZero: gpt-5.5 and every non-OpenAI provider never
// send the field. Its absence must read as 0, not as a parse failure that takes
// the rest of the usage block down with it.
func TestCacheWriteTokensAbsentIsZero(t *testing.T) {
	resp := &Response{}
	a := newResponseAssembler(newStreamAdapter(context.Background(), resp))

	event := []byte(`{
		"type": "response.completed",
		"response": {
			"id": "resp_xyz",
			"usage": {
				"input_tokens": 1000,
				"output_tokens": 50,
				"total_tokens": 1050,
				"input_tokens_details": {"cached_tokens": 900},
				"output_tokens_details": {"reasoning_tokens": 10}
			}
		}
	}`)

	if _, err := a.handleEvent(event); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if got := resp.Usage.CacheWriteTokens; got != 0 {
		t.Errorf("CacheWriteTokens = %d, want 0 when the field is absent", got)
	}
	if got := resp.Usage.CachedTokens; got != 900 {
		t.Errorf("CachedTokens = %d, want 900 — an absent sibling field broke the usage parse", got)
	}
}

// TestUsageWithNoInputTokensDetails is the shape we actually observe from the
// ChatGPT/Codex backend on a cold turn: a large prompt, and no cache accounting
// at all (that backend never reports cache writes — see Usage.CacheWriteTokens).
// The parse must yield zeros and leave the rest of the usage intact rather
// than failing.
func TestUsageWithNoInputTokensDetails(t *testing.T) {
	resp := &Response{}
	a := newResponseAssembler(newStreamAdapter(context.Background(), resp))

	event := []byte(`{
		"type": "response.completed",
		"response": {
			"id": "resp_cold",
			"usage": {
				"input_tokens": 177005,
				"output_tokens": 576,
				"total_tokens": 177581,
				"output_tokens_details": {"reasoning_tokens": 410}
			}
		}
	}`)

	done, err := a.handleEvent(event)
	if err != nil {
		t.Fatalf("a missing input_tokens_details must not fail the parse: %v", err)
	}
	if !done {
		t.Fatal("response.completed did not report done")
	}
	if resp.Usage.CachedTokens != 0 || resp.Usage.CacheWriteTokens != 0 {
		t.Errorf("cache fields = (%d, %d), want (0, 0)", resp.Usage.CachedTokens, resp.Usage.CacheWriteTokens)
	}
	if got := resp.Usage.PromptTokens; got != 177005 {
		t.Errorf("PromptTokens = %d, want 177005 — the missing details block ate the rest of usage", got)
	}
	if got := resp.Usage.ReasoningTokens; got != 410 {
		t.Errorf("ReasoningTokens = %d, want 410", got)
	}
}
