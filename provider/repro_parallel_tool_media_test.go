package provider

import (
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
)

// TestReproParallelToolMediaBreaksToolSequence reproduces the bug where two
// parallel tool calls each returning a media marker produce a message sequence
// like:
//   assistant(tool_calls=[A, B])
//   tool(A)
//   user(synthetic media for A)   <-- breaks the tool block
//   tool(B)
//   user(synthetic media for B)
//
// OpenAI/moonshot rejects this with:
//   "tool_call_ids did not have response messages: <B>"
// because every tool message after `assistant.tool_calls` must appear
// back-to-back with no other role between them.
func TestReproParallelToolMediaBreaksToolSequence(t *testing.T) {
	imgPath := "../img/head.png"

	messages := []Message{
		{Role: "system", Content: "you are imagereader"},
		{Role: "user", Content: "analyze two images"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "read_file:0", Type: "function", Function: FunctionCall{Name: "read_file", Arguments: `{"path":"a"}`}},
				{ID: "read_file:1", Type: "function", Function: FunctionCall{Name: "read_file", Arguments: `{"path":"b"}`}},
			},
		},
		{Role: "tool", ToolCallID: "read_file:0", Content: "ok\n<<media:image/png:" + imgPath + ">>"},
		{Role: "tool", ToolCallID: "read_file:1", Content: "ok\n<<media:image/png:" + imgPath + ">>"},
	}

	out, err := toOpenAIChatMessages(messages, true, false, false)
	if err != nil {
		t.Fatalf("convert err: %v", err)
	}

	// Dump role sequence so the bug is human-readable.
	roles := make([]string, 0, len(out))
	for _, m := range out {
		switch {
		case m.OfSystem != nil:
			roles = append(roles, "system")
		case m.OfUser != nil:
			roles = append(roles, "user")
		case m.OfAssistant != nil:
			roles = append(roles, "assistant")
		case m.OfTool != nil:
			roles = append(roles, "tool("+m.OfTool.ToolCallID+")")
		default:
			roles = append(roles, "unknown")
		}
	}
	t.Logf("role sequence: %s", strings.Join(roles, " -> "))

	// Per OpenAI contract, after the assistant tool_calls message, every tool
	// message must appear before any other role. Here we expect:
	//   ... assistant -> tool(read_file:0) -> tool(read_file:1) -> ...
	// but the buggy converter emits a synthetic user between the two tool
	// messages, which is what triggers the moonshot 400.
	asstIdx := -1
	for i, r := range roles {
		if r == "assistant" {
			asstIdx = i
			break
		}
	}
	if asstIdx < 0 {
		t.Fatalf("no assistant message in output")
	}

	// Walk forward from assistant; collect tool call ids until we see a
	// non-tool role.
	var seenTool []string
	var firstNonToolAfter string
	for i := asstIdx + 1; i < len(roles); i++ {
		r := roles[i]
		if strings.HasPrefix(r, "tool(") {
			seenTool = append(seenTool, r)
			continue
		}
		firstNonToolAfter = r
		break
	}

	if len(seenTool) < 2 {
		t.Errorf("BUG REPRODUCED: only %d tool message(s) appeared back-to-back after assistant; "+
			"first non-tool role between/after is %q. Full sequence: %s",
			len(seenTool), firstNonToolAfter, strings.Join(roles, " -> "))
	}
}

// Avoid unused import warnings if openai-go types ever drift.
var _ = openai.ChatCompletionMessageParamUnion{}
