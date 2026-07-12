package provider

import (
	"encoding/json"
	"reflect"
	"testing"
)

// These tests pin the load-bearing premise of cross-turn previous_response_id
// continuation (the WSPool): the delta gate compares this turn's rebuilt
// input items against a baseline recorded last turn with reflect.DeepEqual,
// so the persistence round trip (session.jsonl is plain JSONL of Message)
// must be conversion-invariant, and the echo updateContinuation synthesizes
// from the live Response must convert identically to the reloaded persisted
// assistant message. If either drifts by one byte, pooling silently stops
// paying: the connection is parked and adopted but every first call falls
// back to full context.
//
// Verified against real session data (5 sessions, ~5400 items, all three
// properties) before this synthetic version was committed; the fixtures
// below cover the shapes that matter — reasoning items with
// encrypted_content, tool calls, media markers, and non-ASCII text.

func continuationFixtureHistory() []Message {
	reasoning := json.RawMessage(`[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"think"}],"encrypted_content":"opaque-blob-≠ascii"}]`)
	return []Message{
		{Role: "user", Content: "look at this 图片", Media: []string{"<<media:image/jpeg:/nonexistent/gone.jpg>>"}},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningDetails: reasoning,
			ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function", Function: FunctionCall{Name: "search", Arguments: `{"q":"猫 café ☕"}`}},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "search", Content: "result: 42\nline2"},
		{Role: "assistant", Content: "答案是 42", ReasoningDetails: reasoning},
		{Role: "user", Content: "thanks"},
	}
}

func jsonlRoundTrip(t *testing.T, msgs []Message) []Message {
	t.Helper()
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal msg %d: %v", i, err)
		}
		if err := json.Unmarshal(b, &out[i]); err != nil {
			t.Fatalf("unmarshal msg %d: %v", i, err)
		}
	}
	return out
}

// TestContinuationSurvivesPersistenceRoundTrip: the daemon-restart path.
// History reloaded from session.jsonl must convert to exactly the items the
// in-memory history converted to when they were sent.
func TestContinuationSurvivesPersistenceRoundTrip(t *testing.T) {
	history := continuationFixtureHistory()
	live := convertMessagesToInputItems(SanitizeMessages(history))
	reloaded := convertMessagesToInputItems(SanitizeMessages(jsonlRoundTrip(t, history)))

	if !reflect.DeepEqual(live, reloaded) {
		t.Fatalf("conversion drifted across the persistence round trip:\nlive:     %+v\nreloaded: %+v", live, reloaded)
	}
}

// TestContinuationEchoMatchesReloadedMessage: updateContinuation's baseline
// tail is synthesized from the live Response fields; the runner persists the
// same fields via AssistantMessageWithTools. Both routes must convert to the
// same items or the next turn's DeepEqual dies on the last element.
func TestContinuationEchoMatchesReloadedMessage(t *testing.T) {
	reasoning := json.RawMessage(`[{"type":"reasoning","id":"rs_2","encrypted_content":"blob"},{"type":"other-provider-shape","x":1}]`)
	resp := &Response{
		Content:          "现在做什么？",
		ReasoningDetails: reasoning,
		ToolCalls: []ToolCall{
			{ID: "call_9", Type: "function", Function: FunctionCall{Name: "dispatch", Arguments: `{}`}},
		},
	}

	echo := assistantMessageToInputItems(Message{
		Content:          resp.Content,
		ReasoningDetails: resp.ReasoningDetails,
		ToolCalls:        resp.ToolCalls,
	})

	persisted := AssistantMessageWithTools(resp.Content, "reasoning text", resp.ReasoningDetails, resp.ToolCalls)
	reloaded := assistantMessageToInputItems(jsonlRoundTrip(t, []Message{persisted})[0])

	if !reflect.DeepEqual(echo, reloaded) {
		t.Fatalf("echo and reloaded conversion diverge:\necho:     %+v\nreloaded: %+v", echo, reloaded)
	}
}

// TestContinuationPrefixExtendsAcrossTurnBoundary: end-to-end shape of the
// cross-turn gate. Baseline = last turn's fullItems + echo; the next turn's
// rebuilt fullItems (reloaded history incl. the persisted reply and trailing
// tool result, plus a new user message) must have the baseline as an exact
// DeepEqual prefix.
func TestContinuationPrefixExtendsAcrossTurnBoundary(t *testing.T) {
	history := continuationFixtureHistory()
	sentItems := convertMessagesToInputItems(SanitizeMessages(history))

	resp := &Response{
		Content:          "",
		ReasoningDetails: json.RawMessage(`[{"type":"reasoning","id":"rs_3","encrypted_content":"tail-blob"}]`),
		ToolCalls: []ToolCall{
			{ID: "call_2", Type: "function", Function: FunctionCall{Name: "dispatch", Arguments: `{"to":"user"}`}},
		},
	}
	echo := assistantMessageToInputItems(Message{
		Content:          resp.Content,
		ReasoningDetails: resp.ReasoningDetails,
		ToolCalls:        resp.ToolCalls,
	})
	baseline := append(append([]map[string]any{}, sentItems...), echo...)

	// Next turn: history + persisted reply + its tool result + new user msg,
	// all through the persistence round trip.
	nextHistory := append(append([]Message{}, history...),
		AssistantMessageWithTools(resp.Content, "", resp.ReasoningDetails, resp.ToolCalls),
		ToolResultMessage("call_2", "dispatch", "delivered"),
	)
	nextHistory = jsonlRoundTrip(t, nextHistory)
	nextHistory = append(nextHistory, Message{Role: "user", Content: "next question"})

	fullItems := convertMessagesToInputItems(SanitizeMessages(nextHistory))
	if len(fullItems) < len(baseline) {
		t.Fatalf("rebuilt items (%d) shorter than baseline (%d)", len(fullItems), len(baseline))
	}
	if !reflect.DeepEqual(fullItems[:len(baseline)], baseline) {
		for i := range baseline {
			if !reflect.DeepEqual(fullItems[i], baseline[i]) {
				t.Fatalf("prefix diverges at item %d:\nbaseline: %+v\nrebuilt:  %+v", i, baseline[i], fullItems[i])
			}
		}
		t.Fatal("prefix diverges (length mismatch inside compared range)")
	}
	if len(fullItems) == len(baseline) {
		t.Fatal("expected a non-empty delta tail (tool result + user message)")
	}
}
