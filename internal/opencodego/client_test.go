package opencodego

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const chatEnd = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestChatToolLoop(t *testing.T) {
	for _, model := range Models() {
		if model.ID == "claude-haiku-5-5" || model.ID == "qwen3.8-flash" {
			continue
		}
		t.Run(model.ID, func(t *testing.T) {
			effort := model.Efforts[len(model.Efforts)-1]
			var turn int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				turn++
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer key" || r.Header.Get("x-opencode-session") != "session" || r.Header.Get("User-Agent") != "nagobot/1.0" {
					t.Errorf("bad request identity: %s %+v", r.URL.Path, r.Header)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != model.ID || body["reasoning_effort"] != effort || body["max_tokens"] != float64(200) {
					t.Errorf("bad parameters: %+v", body)
				}
				if _, ok := body["thinking"]; ok {
					t.Error("native thinking leaked into gateway chat")
				}
				if strings.HasPrefix(model.ID, "glm-") {
					if body["temperature"] != float64(1) {
						t.Error("missing GLM temperature")
					}
				} else if _, ok := body["temperature"]; ok {
					t.Error("unexpected temperature")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, ": keep-alive\n\n\nevent: ping\n\ndata:\n\n: keep-alive\r\n\r\n")
				if turn == 1 {
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"check\",\"tool_calls\":[{\"index\":1,\"id\":\"b\",\"function\":{\"name\":\"second\",\"arguments\":\"{}\"}},{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\n\n")
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"ok\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
					fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120,\"prompt_cache_hit_tokens\":80,\"completion_tokens_details\":{\"reasoning_tokens\":4}}}\n\ndata: [DONE]\n\n")
				} else {
					messages := body["messages"].([]any)
					assistant := messages[1].(map[string]any)
					if assistant["reasoning_content"] != "check" || messages[2].(map[string]any)["tool_call_id"] != "a" {
						t.Errorf("lost tool history: %+v", messages)
					}
					if strings.HasPrefix(model.ID, "deepseek-") && messages[4].(map[string]any)["reasoning_content"] != "" {
						t.Error("DeepSeek empty reasoning key omitted")
					}
					fmt.Fprint(w, chatEnd)
				}
			}))
			defer server.Close()
			client := NewClient("key", server.URL+"/v1/chat/completions/")
			req := Request{Model: model.ID, Effort: effort, MaxTokens: 200, Messages: []Message{{Role: "user", Content: "check"}}, Tools: []Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}}
			var reasoning, tool bool
			response, err := client.Chat(context.Background(), "session", req, func(d Delta) { reasoning = reasoning || d.Type == "reasoning"; tool = tool || d.Type == "tool_call" })
			if err != nil {
				t.Fatal(err)
			}
			if !reasoning || !tool || len(response.ToolCalls) != 2 || response.ToolCalls[0].ID != "a" || response.ToolCalls[0].Arguments != `{"q":"ok"}` || response.ToolCalls[1].ID != "b" {
				t.Fatalf("bad tool stream: %+v", response)
			}
			if response.Usage != (Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CachedTokens: 80, ReasoningTokens: 4}) {
				t.Fatalf("usage duplicated or lost: %+v", response.Usage)
			}
			req.Messages = append(req.Messages, Message{Role: "assistant", ReasoningContent: response.ReasoningContent, ToolCalls: response.ToolCalls}, Message{Role: "tool", ToolCallID: "a", Content: "ok"}, Message{Role: "tool", ToolCallID: "b", Content: "ok"}, Message{Role: "assistant", Content: "previous"}, Message{Role: "user", Content: "continue"})
			response, err = client.Chat(context.Background(), "session", req, nil)
			if err != nil || response.Content != "done" {
				t.Fatalf("continuation: %+v %v", response, err)
			}
		})
	}
}

func messageEvent(w io.Writer, kind, payload string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, payload)
}

func TestMessagesSignedToolLoop(t *testing.T) {
	var turn int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "thinking-binding-controls-2026-08-01" || r.Header.Get("Authorization") != "" {
			t.Errorf("bad Messages auth: %s %+v", r.URL.Path, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["system"] != "system" || body["max_tokens"] != float64(100) || body["thinking"].(map[string]any)["type"] != "adaptive" {
			t.Errorf("bad Messages config: %+v", body)
		}
		if body["thinking"].(map[string]any)["block_binding"].(map[string]any)["prefix_mismatch_behavior"] != "drop_block" {
			t.Error("missing prefix mismatch control")
		}
		for _, key := range []string{"temperature", "reasoning_effort", "top_p", "top_k", "stream_options"} {
			if _, exists := body[key]; exists {
				t.Errorf("chat/native parameter leaked: %s", key)
			}
		}
		if turn == 1 {
			if _, exists := body["output_config"]; exists {
				t.Error("bare Haiku must preserve server effort default")
			}
		} else {
			if body["output_config"].(map[string]any)["effort"] != "low" {
				t.Error("wrong Haiku effort")
			}
			messages := body["messages"].([]any)
			if len(messages) != 3 {
				t.Errorf("adjacent roles not merged: %+v", messages)
			}
			thinking := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if thinking["signature"] != "signed" || thinking["thinking"] != "" {
				t.Errorf("empty signed thinking lost: %+v", thinking)
			}
			redacted := messages[1].(map[string]any)["content"].([]any)[1].(map[string]any)
			if redacted["data"] != "encrypted" {
				t.Error("redacted thinking lost")
			}
			userBlocks := messages[2].(map[string]any)["content"].([]any)
			if len(userBlocks) != 2 || userBlocks[0].(map[string]any)["type"] != "tool_result" {
				t.Errorf("tool/user merge incorrect: %+v", userBlocks)
			}
			parts := userBlocks[0].(map[string]any)["content"].([]any)
			if len(parts) != 2 || parts[1].(map[string]any)["type"] != "image" {
				t.Error("tool image not in tool_result content")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\n")
		messageEvent(w, "message_start", `{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":80,"cache_creation_input_tokens":10}}}`)
		if turn == 1 {
			messageEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
			messageEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed"}}`)
			messageEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
			messageEvent(w, "content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"encrypted"}}`)
			messageEvent(w, "content_block_stop", `{"type":"content_block_stop","index":1}`)
			messageEvent(w, "content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"a","name":"lookup","input":{}}}`)
			messageEvent(w, "content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`)
			messageEvent(w, "content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"ok\"}"}}`)
			messageEvent(w, "content_block_stop", `{"type":"content_block_stop","index":2}`)
			messageEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`)
		} else {
			messageEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			messageEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`)
			messageEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
			messageEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)
		}
		messageEvent(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer server.Close()
	client := NewClient("key", server.URL+"/v1/messages")
	req := Request{Model: "claude-haiku-5-5", MaxTokens: 100, Messages: []Message{{Role: "system", Content: "system"}, {Role: "user", Content: "check"}}}
	response, err := client.Chat(context.Background(), "session", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.ReasoningContent != "" || len(response.ReasoningDetails) == 0 || len(response.ToolCalls) != 1 || response.ToolCalls[0].Arguments != `{"q":"ok"}` {
		t.Fatalf("invalid Messages tool result: %+v", response)
	}
	if response.Usage != (Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CachedTokens: 80, CacheWriteTokens: 10}) {
		t.Fatalf("invalid Messages usage: %+v", response.Usage)
	}
	req.Effort = "low"
	req.Messages = append(req.Messages, Message{Role: "assistant", ReasoningDetails: response.ReasoningDetails, ToolCalls: response.ToolCalls}, Message{Role: "tool", ToolCallID: "a", Content: "ok", Images: []Image{{MIMEType: "image/png", Data: "eA=="}}}, Message{Role: "user", Content: "continue"})
	response, err = client.Chat(context.Background(), "session", req, nil)
	if err != nil || response.Content != "done" {
		t.Fatalf("Messages continuation: %+v %v", response, err)
	}
}

func TestStreamsRejectInvalidAndTruncatedData(t *testing.T) {
	cases := []struct{ name, model, payload string }{
		{"malformed", "glm-5.3", "data: {\"choices\":\n\n"},
		{"chat_eof", "glm-5.3", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"},
		{"no_finish", "glm-5.3", "data: [DONE]\n\n"},
		{"no_done", "glm-5.3", strings.Split(chatEnd, "data: [DONE]")[0]},
		{"partial_tool", "glm-5.3", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"bad\",\"arguments\":\"{\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"},
		{"tool_length", "glm-5.3", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"bad\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"},
		{"stream_error", "glm-5.3", "event: error\ndata: {\"error\":{\"message\":\"failure\"}}\n\n"},
		{"messages_eof", "claude-haiku-5-5", "event: ping\ndata: {\"type\":\"ping\"}\n\n"},
		{"messages_no_stop_reason", "claude-haiku-5-5", "data: {\"type\":\"message_stop\"}\n\n"},
		{"messages_incomplete_block", "claude-haiku-5-5", "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"partial\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"messages_tool_max_tokens", "claude-haiku-5-5", "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"a\",\"name\":\"bad\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
	}
	// Qwen uses the same Messages framing but a different reasoning policy;
	// incomplete/length-stopped tool calls must remain errors on both paths.
	for _, test := range cases {
		if test.model == "claude-haiku-5-5" {
			test.name = "qwen_" + test.name
			test.model = "qwen3.8-flash"
			cases = append(cases, test)
		}
		if test.model == "glm-5.3" {
			test.name = "step_" + test.name
			test.model = "step-5-preview-free"
			cases = append(cases, test)
		}
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var attempts int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, test.payload)
			}))
			defer server.Close()
			response, err := NewClient("key", server.URL).Chat(context.Background(), "", Request{Model: test.model}, nil)
			if err == nil || response != nil || attempts != 1 {
				t.Fatalf("bad stream treated as success or retried: response=%+v err=%v attempts=%d", response, err, attempts)
			}
		})
	}
}

func TestRetryPolicy(t *testing.T) {
	for _, status := range []int{400, 401, 408, 409, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var count int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if count < 3 || status == 400 || status == 401 {
					w.WriteHeader(status)
					fmt.Fprint(w, `{"error":"rejected"}`)
					return
				}
				fmt.Fprint(w, chatEnd)
			}))
			defer server.Close()
			_, err := NewClient("key", server.URL).Chat(context.Background(), "", Request{Model: "glm-5.3"}, nil)
			if status == 400 || status == 401 {
				var httpErr *HTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != status || count != 1 {
					t.Fatalf("nonretry HTTP: err=%v count=%d", err, count)
				}
			} else if err != nil || count != 3 {
				t.Fatalf("retry: err=%v count=%d", err, count)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed *atomic.Bool
}

func (b trackedBody) Close() error { b.closed.Store(true); return nil }

func TestTransportRetryAndBodyClosed(t *testing.T) {
	client := NewClient("key", "")
	var count int
	var closed atomic.Bool
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count++
		if count < 3 {
			return nil, fmt.Errorf("connection reset")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: trackedBody{Reader: strings.NewReader(chatEnd), closed: &closed}}, nil
	})}
	if _, err := client.Chat(context.Background(), "", Request{Model: "glm-5.3"}, nil); err != nil || count != 3 || !closed.Load() {
		t.Fatalf("transport retries/body close: %v %d %v", err, count, closed.Load())
	}
}

func TestRetryLimitAndErrorBodyClosed(t *testing.T) {
	client := NewClient("key", "")
	var attempts int
	var closes atomic.Int32
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: &countedCloseBody{Reader: strings.NewReader("unavailable"), closes: &closes}}, nil
	})}
	_, err := client.Chat(context.Background(), "", Request{Model: "glm-5.3"}, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 503 || attempts != 3 || closes.Load() != 3 {
		t.Fatalf("retry cap/close: err=%v attempts=%d closes=%d", err, attempts, closes.Load())
	}
}

type countedCloseBody struct {
	io.Reader
	closes *atomic.Int32
}

func (b *countedCloseBody) Close() error { b.closes.Add(1); return nil }

func TestRedirectCannotMoveCredentialsToAnotherEndpoint(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true); fmt.Fprint(w, chatEnd) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/messages", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	_, err := NewClient("key", origin.URL).Chat(context.Background(), "", Request{Model: "claude-haiku-5-5"}, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 307 || redirected.Load() {
		t.Fatalf("redirect followed: err=%v redirected=%v", err, redirected.Load())
	}
}

func TestMessagesOnlyReplaysValidSignedThinkingBlocks(t *testing.T) {
	details := json.RawMessage(`[{"type":"reasoning","encrypted_content":"openai"},{"functionCall":{"name":"gemini"},"thoughtSignature":"foreign"},{"type":"thinking","thinking":"unsigned"},{"type":"thinking","thinking":"","signature":"signed"},{"type":"redacted_thinking","data":"encrypted"}]`)
	request, err := messagesRequest(Request{Model: "claude-haiku-5-5", Messages: []Message{{Role: "assistant", ReasoningDetails: details, Content: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 1 || len(request.Messages[0].Content) != 3 {
		t.Fatalf("foreign or unsigned block replayed: %+v", request.Messages)
	}
	if !strings.Contains(string(request.Messages[0].Content[0]), "signed") || !strings.Contains(string(request.Messages[0].Content[1]), "encrypted") {
		t.Fatalf("signed blocks lost: %+v", request.Messages[0].Content)
	}
}

func TestCancellationStopsRequest(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := NewClient("key", server.URL).Chat(ctx, "", Request{Model: "glm-5.3"}, nil)
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wrong cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request not cancelled")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream body not released")
	}
}

func TestChatToolImagesWaitForAllToolResults(t *testing.T) {
	request, err := chatRequest(Request{Model: "glm-5.3-flash", Messages: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "first", Arguments: `{}`}, {ID: "b", Name: "second", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "a", Content: "one", Images: []Image{{MIMEType: "image/png", Data: "eA=="}}},
		{Role: "tool", ToolCallID: "b", Content: "two"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 4 || request.Messages[1].Role != "tool" || request.Messages[2].Role != "tool" || request.Messages[3].Role != "user" {
		t.Fatalf("image split tool sequence: %+v", request.Messages)
	}
	if _, ok := request.Messages[1].Content.(string); !ok {
		t.Fatal("tool content must be string")
	}
}

func TestSSEMultilineAndCRLF(t *testing.T) {
	response, err := readChat(strings.NewReader(": keep-alive\r\n\r\ndata: {\"choices\":\r\ndata: [{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n"), func(Delta) {})
	if err != nil || response.Content != "ok" {
		t.Fatalf("multiline: %+v %v", response, err)
	}
}

func TestModelValidationAndCatalogIsolation(t *testing.T) {
	models := Models()
	models[0].Efforts[0] = "corrupted"
	info, ok := ModelInfo("glm-5.3-flash")
	if !ok || info.Efforts[0] != "low" {
		t.Fatal("catalog exposed mutable slice")
	}
	for _, req := range []Request{{Model: "unrecognized"}, {Model: "deepseek-v4-pro", Effort: "low"}, {Model: "glm-5.3", Messages: []Message{{Role: "user", Images: []Image{{}}}}}} {
		if _, err := NewClient("key", "").Chat(context.Background(), "", req, nil); err == nil {
			t.Fatalf("invalid model request accepted: %+v", req)
		}
	}
	for _, role := range []string{"invalid", "developer"} {
		if _, err := chatRequest(Request{Messages: []Message{{Role: role}}}); err == nil {
			t.Errorf("invalid role accepted: %s", role)
		}
	}
	if _, err := messagesRequest(Request{Messages: []Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "bad", Arguments: `{`}}}}}); err == nil {
		t.Error("invalid historical arguments accepted")
	}
}
