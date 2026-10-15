package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/config"
)

func TestOpenCodeGoQwenToolLoop(t *testing.T) {
	for _, effort := range []string{"", "none", "low", "medium", "xhigh"} {
		t.Run("effort="+effort, func(t *testing.T) {
			turn := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				turn++
				if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "go-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-beta") != "" {
					t.Errorf("Qwen endpoint/authentication: %s %+v", r.URL.Path, r.Header)
				}
				if r.Header.Get("x-opencode-session") != openCodeGoTestSessionID("cli:qwen") || r.Header.Get("User-Agent") != "nagobot/1.0" {
					t.Error("Qwen lost gateway identity")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				thinking := body["thinking"].(map[string]any)
				wantThinking := "enabled"
				if effort == "none" {
					wantThinking = "disabled"
				}
				if len(thinking) != 1 || thinking["type"] != wantThinking || body["model"] != "qwen3.8-flash" || body["system"] != "system" || body["max_tokens"] != float64(2048) || body["stream"] != true {
					t.Errorf("Qwen policy: %+v", body)
				}
				if effort == "" || effort == "none" {
					if _, exists := body["output_config"]; exists {
						t.Error("bare/disabled Qwen must omit effort")
					}
				} else if body["output_config"].(map[string]any)["effort"] != effort {
					t.Error("Qwen effort was not sent")
				}
				for _, field := range []string{"reasoning_effort", "stream_options", "temperature", "top_p", "top_k"} {
					if _, exists := body[field]; exists {
						t.Errorf("foreign parameter %s", field)
					}
				}
				messages := body["messages"].([]any)
				if turn == 2 {
					assistant := messages[1].(map[string]any)["content"].([]any)
					if effort != "none" {
						thought := assistant[0].(map[string]any)
						if thought["thinking"] != "check" || thought["signature"] != "" {
							t.Errorf("unsigned reasoning not replayed: %+v", thought)
						}
					}
					tool := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
					if tool["type"] != "tool_result" || tool["tool_use_id"] != "call_qwen" || tool["content"] != "42" {
						t.Errorf("tool continuation: %+v", tool)
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				event := func(kind, fields string) {
					fmt.Fprintf(w, "event: %s\ndata: {\"type\":%q,%s}\n\n", kind, kind, fields)
					fmt.Fprint(w, ": keep-alive\r\n\r\n")
				}
				event("message_start", `"message":{"usage":{"input_tokens":10,"output_tokens":0}}`)
				if turn == 1 {
					if effort != "none" {
						event("content_block_start", `"index":0,"content_block":{"type":"thinking","thinking":"","signature":""}`)
						event("content_block_delta", `"index":0,"delta":{"type":"thinking_delta","thinking":"check"}`)
						event("content_block_delta", `"index":0,"delta":{"type":"signature_delta","signature":""}`)
						event("content_block_stop", `"index":0`)
					}
					event("content_block_start", `"index":1,"content_block":{"type":"tool_use","id":"call_qwen","name":"add","input":{}}`)
					event("content_block_delta", `"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":17,"}`)
					event("content_block_delta", `"index":1,"delta":{"type":"input_json_delta","partial_json":"\"b\":25}"}`)
					event("content_block_stop", `"index":1`)
					event("message_delta", `"delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":80,"cache_creation_input_tokens":20}`)
				} else {
					event("content_block_start", `"index":0,"content_block":{"type":"text","text":""}`)
					event("content_block_delta", `"index":0,"delta":{"type":"text_delta","text":"42"}`)
					event("content_block_stop", `"index":0`)
					event("message_delta", `"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}`)
				}
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			t.Setenv("OPENCODE_GO_API_KEY", "go-key")
			t.Setenv("OPENCODE_GO_API_BASE", server.URL+"/v1")
			model := "qwen3.8-flash"
			if effort != "" {
				model += "[" + effort + "]"
			}
			cfg := &config.Config{Thread: config.ThreadConfig{Provider: "opencode-go", ModelType: model, MaxTokens: 2048}}
			factory, err := NewFactory(func() *config.Config { return cfg })
			if err != nil {
				t.Fatal(err)
			}
			defer factory.Close()
			p, err := factory.Create("", "")
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithSessionKey(context.Background(), "cli:qwen")
			req := &Request{Messages: []Message{{Role: "system", Content: "system"}, {Role: "user", Content: "add"}}, Tools: []ToolDef{{Type: "function", Function: FunctionDef{Name: "add", Parameters: map[string]any{"type": "object"}}}}}
			result, err := p.Chat(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			var reasoning strings.Builder
			for {
				d, err := result.(StreamChatResult).Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if d.Type == DeltaReasoning {
					reasoning.WriteString(d.Text)
				}
			}
			resp, err := result.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Arguments != `{"a":17,"b":25}` || resp.Usage.PromptTokens != 200 || resp.Usage.CachedTokens != 80 || resp.Usage.CacheWriteTokens != 20 || resp.Usage.CompletionTokens != 20 || len(resp.ReasoningDetails) != 0 {
				t.Fatalf("invalid Qwen tool stream: %+v", resp)
			}
			if effort != "none" && (resp.ReasoningContent != "check" || reasoning.String() != "check") {
				t.Fatal("unsigned thinking lost")
			}
			req.Messages = append(req.Messages, Message{Role: "assistant", ReasoningContent: resp.ReasoningContent, ToolCalls: resp.ToolCalls}, Message{Role: "tool", ToolCallID: resp.ToolCalls[0].ID, Content: "42"})
			result, err = p.Chat(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			resp, err = result.Wait()
			if err != nil || resp.Content != "42" || resp.ModelLabel != "qwen3.8-flash" || resp.ProviderLabel != "opencode-go" {
				t.Fatalf("continuation: %+v %v", resp, err)
			}
		})
	}
}
