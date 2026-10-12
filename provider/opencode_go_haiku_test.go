package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestOpenCodeGoHaikuToolLoop(t *testing.T) {
	requests := make(chan openCodeGoWireRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- openCodeGoWireRequest{header: r.Header.Clone(), body: body, path: r.URL.Path}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\n")
		event := func(kind, fields string) {
			fmt.Fprintf(w, "event: %s\ndata: {\"type\":%q,%s}\n\n", kind, kind, fields)
		}
		event("message_start", `"message":{"id":"msg_1","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":80,"cache_creation_input_tokens":20}}`)
		if len(body["messages"].([]any)) == 1 {
			// Empty thinking text still has a signature needed for tool replay.
			event("content_block_start", `"index":0,"content_block":{"type":"thinking","thinking":"","signature":""}`)
			event("content_block_delta", `"index":0,"delta":{"type":"signature_delta","signature":"signed-empty-thinking"}`)
			event("content_block_stop", `"index":0`)
			event("content_block_start", `"index":1,"content_block":{"type":"tool_use","id":"call_haiku","name":"lookup","input":{}}`)
			event("content_block_delta", `"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}`)
			event("ping", `"keep_alive":true`)
			event("content_block_delta", `"index":1,"delta":{"type":"input_json_delta","partial_json":"\"ok\"}"}`)
			event("content_block_stop", `"index":1`)
			event("message_delta", `"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}`)
		} else {
			event("content_block_start", `"index":0,"content_block":{"type":"text","text":""}`)
			event("content_block_delta", `"index":0,"delta":{"type":"text_delta","text":"done"}`)
			event("content_block_stop", `"index":0`)
			event("message_delta", `"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}`)
		}
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	p := newOpenCodeGoProvider("go-key", server.URL+"/v1/chat/completions", "claude-haiku-5-5[low]", "", 1024)
	ctx := WithSessionKey(context.Background(), "cli:haiku")
	req := &Request{Messages: []Message{{Role: "system", Content: "initial system"}, {Role: "user", Content: "lookup"}},
		Tools: []ToolDef{{Type: "function", Function: FunctionDef{Name: "lookup", Parameters: map[string]any{"type": "object"}}}}}
	result, err := p.Chat(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	stream := result.(StreamChatResult)
	sawTool := false
	for {
		delta, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		sawTool = sawTool || delta.Type == DeltaToolCall && delta.ToolName == "lookup"
	}
	resp, err := result.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !sawTool || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Arguments != `{"q":"ok"}` {
		t.Fatalf("Haiku tool call was not accumulated: %+v", resp)
	}
	if resp.ReasoningContent != "" || !strings.Contains(string(resp.ReasoningDetails), "signed-empty-thinking") {
		t.Fatal("signed empty thinking block was lost")
	}
	if resp.Usage.PromptTokens != 200 || resp.Usage.CachedTokens != 80 || resp.Usage.CacheWriteTokens != 20 || resp.Usage.CompletionTokens != 20 {
		t.Fatalf("Haiku usage incorrectly normalized: %+v", resp.Usage)
	}
	wire := <-requests
	if wire.path != "/v1/messages" || wire.header.Get("x-api-key") != "go-key" || wire.header.Get("Authorization") != "" || wire.header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("Haiku used wrong endpoint or authentication: path=%s", wire.path)
	}
	if wire.body["output_config"].(map[string]any)["effort"] != "low" || wire.body["system"] == nil || wire.body["model"] != "claude-haiku-5-5" {
		t.Fatalf("Haiku request incorrectly encoded: %+v", wire.body)
	}
	for _, field := range []string{"reasoning_effort", "stream_options", "temperature", "top_p", "top_k"} {
		if _, ok := wire.body[field]; ok {
			t.Errorf("Haiku received Chat Completions field %q", field)
		}
	}
	req.Messages[0].Content = "changed system after compression"
	req.Messages = append(req.Messages,
		Message{Role: "assistant", ToolCalls: resp.ToolCalls, ReasoningDetails: resp.ReasoningDetails},
		Message{Role: "tool", ToolCallID: resp.ToolCalls[0].ID, Content: "ok"})
	result, err = p.Chat(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = result.Wait()
	if err != nil || resp.Content != "done" || resp.ModelLabel != "claude-haiku-5-5" || resp.ProviderLabel != "opencode-go" {
		t.Fatalf("Haiku continuation failed: %+v, %v", resp, err)
	}
	next := <-requests
	if next.header.Get("x-opencode-session") != wire.header.Get("x-opencode-session") {
		t.Fatal("Haiku continuation changed gateway session")
	}
	messages := next.body["messages"].([]any)
	assistant := messages[1].(map[string]any)["content"].([]any)
	if assistant[0].(map[string]any)["signature"] != "signed-empty-thinking" || assistant[1].(map[string]any)["id"] != "call_haiku" {
		t.Fatalf("Haiku signed/tool blocks were not replayed in order: %+v", assistant)
	}
	tool := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if tool["type"] != "tool_result" || tool["tool_use_id"] != "call_haiku" {
		t.Fatal("Haiku tool result was not wired to its invocation")
	}
	thinking := next.body["thinking"].(map[string]any)
	if thinking["block_binding"].(map[string]any)["prefix_mismatch_behavior"] != "drop_block" || !strings.Contains(next.header.Get("anthropic-beta"), "thinking-binding-controls-2026-08-01") {
		t.Fatal("changed prompt does not have binding mismatch controls")
	}
}

// This boundary is part of the provider contract: protocol-compatible models
// must not silently start using another provider's SDK or helper functions.
func TestOpenCodeGoProviderOwnsItsSDKBoundary(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "opencode_go.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	importsSDK := false
	for _, item := range file.Imports {
		path, _ := strconv.Unquote(item.Path.Value)
		importsSDK = importsSDK || path == "github.com/linanwx/nagobot/internal/opencodego"
		if strings.Contains(path, "openai") || strings.Contains(path, "anthropic") {
			t.Errorf("OpenCode Go imports a foreign provider SDK: %s", path)
		}
	}
	if !importsSDK {
		t.Fatal("OpenCode Go does not use its dedicated SDK")
	}
	forbidden := map[string]bool{
		"openAIStreamChat": true, "toOpenAIChatMessages": true, "toOpenAIChatTools": true,
		"fromOpenAIChatToolCalls": true, "extractReasoningText": true,
		"parseModelEffort": true, "sessionCacheKey": true, "normalizeSDKBaseURL": true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if name, ok := call.Fun.(*ast.Ident); ok && forbidden[name.Name] {
				t.Errorf("OpenCode Go calls foreign provider implementation %s", name.Name)
			}
		}
		return true
	})
}
