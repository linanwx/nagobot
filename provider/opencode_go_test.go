package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
)

type openCodeGoWireRequest struct {
	header http.Header
	body   map[string]any
	path   string
}

func openCodeGoTestServer(t *testing.T, cacheField string) (*httptest.Server, <-chan openCodeGoWireRequest) {
	t.Helper()
	requests := make(chan openCodeGoWireRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- openCodeGoWireRequest{header: r.Header.Clone(), body: body, path: r.URL.Path}
		// Model the strict Go upstream that rejected real production requests.
		// Native-provider extensions must not leak into this gateway request.
		if _, exists := body["thinking"]; exists {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"param":"thinking","type":"invalid_request_error","message":"Upstream request failed: [unknown_parameter] invalid request body: json: unknown field \"thinking\""}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// Go can send keep-alives while the upstream is preparing a response.
		fmt.Fprint(w, ": keep-alive\n\n\nevent: ping\n\ndata:\n\n")
		writeChunk := func(choices string) {
			fmt.Fprintf(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"test\",\"choices\":%s}\n\n", choices)
			fmt.Fprint(w, ": keep-alive\r\n\r\n")
		}
		if len(body["messages"].([]any)) == 1 {
			writeChunk(`[{"index":0,"delta":{"role":"assistant","reasoning_content":"check first"}}]`)
			writeChunk(`[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]`)
			writeChunk(`[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ok\"}"}}]},"finish_reason":"tool_calls"}]`)
		} else {
			writeChunk(`[{"index":0,"delta":{"role":"assistant","content":"all "}}]`)
			writeChunk(`[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]`)
		}
		fmt.Fprintf(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120,%s,\"completion_tokens_details\":{\"reasoning_tokens\":10}}}\n\ndata: [DONE]\n\n", cacheField)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestOpenCodeGoToolLoop(t *testing.T) {
	for _, test := range []struct{ model, effort string }{
		{"glm-5.3-flash", "max"}, {"glm-5.3", "max"},
		{"deepseek-v4.1-flash", "max"}, {"deepseek-v4-pro", "max"},
		{"step-5-preview-free", ""}, {"step-5-preview-free", "low"},
		{"step-5-preview-free", "medium"}, {"step-5-preview-free", "high"},
	} {
		model, effort := test.model, test.effort
		modelType := model
		if effort != "" {
			modelType += "[" + effort + "]"
		}
		t.Run(modelType, func(t *testing.T) {
			cache := `"prompt_tokens_details":{"cached_tokens":80}`
			if strings.HasPrefix(model, "deepseek-") {
				cache = `"prompt_cache_hit_tokens":80`
			}
			server, requests := openCodeGoTestServer(t, cache)
			t.Setenv("OPENCODE_GO_API_KEY", "test-key")
			// Also exercise normalization of a pasted full endpoint URL.
			t.Setenv("OPENCODE_GO_API_BASE", server.URL+"/v1/chat/completions/")
			cfg := &config.Config{Thread: config.ThreadConfig{
				Provider: "opencode-go", ModelType: modelType, MaxTokens: 1234, Temperature: 0.3,
			}}
			factory, err := NewFactory(func() *config.Config { return cfg })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(factory.Close)
			p, err := factory.Create("", "")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(WithSessionKey(context.Background(), "telegram:123"), 5*time.Second)
			defer cancel()
			req := &Request{Messages: []Message{{Role: "user", Content: "check it"}}, Tools: []ToolDef{{
				Type: "function", Function: FunctionDef{Name: "lookup", Parameters: map[string]any{"type": "object"}},
			}}}
			result, err := p.Chat(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			stream := result.(StreamChatResult)
			var reasoning, tool bool
			for {
				delta, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				reasoning = reasoning || (delta.Type == DeltaReasoning && delta.Text == "check first")
				tool = tool || (delta.Type == DeltaToolCall && delta.ToolName == "lookup")
			}
			resp, err := result.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if !reasoning || !tool || resp.ReasoningContent != "check first" {
				t.Fatalf("missing streamed reasoning/tool: reasoning=%v tool=%v response=%+v", reasoning, tool, resp)
			}
			if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Arguments != `{"q":"ok"}` {
				t.Fatalf("tool arguments were not accumulated: %+v", resp.ToolCalls)
			}
			if resp.ProviderLabel != "opencode-go" || resp.ModelLabel != model || resp.Usage != (Usage{
				PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CachedTokens: 80, ReasoningTokens: 10,
			}) {
				t.Fatalf("incorrect provider/model/usage: %+v", resp)
			}
			wire := <-requests
			if wire.path != "/v1/chat/completions" || wire.header.Get("Authorization") != "Bearer test-key" || wire.header.Get("User-Agent") != "nagobot/1.0" {
				t.Fatalf("incorrect endpoint or identity: %+v", wire)
			}
			if wire.header.Get("x-opencode-session") != openCodeGoTestSessionID("telegram:123") {
				t.Fatal("missing stable hashed session header")
			}
			if wire.body["model"] != model || wire.body["stream"] != true || wire.body["max_tokens"] != float64(1234) {
				t.Fatalf("incorrect model or generation parameters: %+v", wire.body)
			}
			if effort == "" {
				if _, exists := wire.body["reasoning_effort"]; exists {
					t.Fatal("bare model must preserve server effort default")
				}
			} else if wire.body["reasoning_effort"] != effort {
				t.Fatalf("wrong reasoning effort: %+v", wire.body)
			}
			if wire.body["stream_options"].(map[string]any)["include_usage"] != true {
				t.Fatalf("missing usage request: %+v", wire.body)
			}
			if strings.HasPrefix(model, "glm-") {
				if wire.body["temperature"] != float64(1) {
					t.Fatalf("incorrect GLM temperature: %+v", wire.body)
				}
			} else if _, ok := wire.body["temperature"]; ok {
				t.Fatal("unexpected temperature")
			}
			for _, field := range []string{"provider", "extra_body", "store", "thinking"} {
				if _, ok := wire.body[field]; ok {
					t.Errorf("unexpected other-provider parameter %q", field)
				}
			}
			req.Messages = append(req.Messages,
				Message{Role: "assistant", ReasoningContent: resp.ReasoningContent, ToolCalls: resp.ToolCalls},
				Message{Role: "tool", ToolCallID: resp.ToolCalls[0].ID, Content: "ok"},
				Message{Role: "assistant", Content: "previous answer"},
				Message{Role: "user", Content: "continue"},
			)
			// A new turn-scoped provider must retain the same conversation header.
			p, err = factory.Create("", "")
			if err != nil {
				t.Fatal(err)
			}
			result, err = p.Chat(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			resp, err = result.Wait()
			if err != nil || resp.Content != "all done" {
				t.Fatalf("tool continuation failed: response=%+v err=%v", resp, err)
			}
			next := <-requests
			if next.header.Get("x-opencode-session") != wire.header.Get("x-opencode-session") {
				t.Fatal("session header changed across turns")
			}
			messages := next.body["messages"].([]any)
			if messages[1].(map[string]any)["reasoning_content"] != "check first" || messages[2].(map[string]any)["tool_call_id"] != "call_1" {
				t.Fatalf("tool history was not replayed: %+v", messages)
			}
			if strings.HasPrefix(model, "deepseek-") && messages[3].(map[string]any)["reasoning_content"] != "" {
				t.Fatal("DeepSeek assistant with no thinking must carry an empty reasoning_content")
			}
		})
	}
}

func TestOpenCodeGoSessionIsolation(t *testing.T) {
	server, requests := openCodeGoTestServer(t, `"prompt_tokens_details":{"cached_tokens":0}`)
	p := newOpenCodeGoProvider("key", server.URL, "glm-5.3", "", 100)
	var wg sync.WaitGroup
	for _, key := range []string{"cli:first", "cli:second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(WithSessionKey(context.Background(), key), 5*time.Second)
			defer cancel()
			result, err := p.Chat(ctx, &Request{Messages: []Message{{Role: "user", Content: key}}})
			if err == nil {
				_, err = result.Wait()
			}
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	first, second := <-requests, <-requests
	if first.header.Get("x-opencode-session") == second.header.Get("x-opencode-session") {
		t.Fatal("concurrent conversations shared a routing header")
	}
	for _, wire := range []openCodeGoWireRequest{first, second} {
		key := wire.body["messages"].([]any)[0].(map[string]any)["content"].(string)
		if wire.header.Get("x-opencode-session") != openCodeGoTestSessionID(key) {
			t.Fatal("concurrent request used another conversation's header")
		}
		if _, ok := wire.body["reasoning_effort"]; ok {
			t.Fatal("bare model must leave reasoning effort at the server default")
		}
	}
	for range 2 {
		result, err := p.Chat(context.Background(), &Request{Messages: []Message{{Role: "user", Content: "standalone"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := result.Wait(); err != nil {
			t.Fatal(err)
		}
		if got := (<-requests).header.Get("x-opencode-session"); got == "" || got != p.fallbackSessionID {
			t.Fatal("standalone request lacks a stable fallback session")
		}
	}
}

func TestOpenCodeGoModelCapabilities(t *testing.T) {
	for _, model := range []string{"glm-5.3-flash", "glm-5.3", "deepseek-v4.1-flash", "deepseek-v4-pro", "claude-haiku-5-5", "qwen3.8-flash", "step-5-preview-free"} {
		if err := ValidateProviderModelType("opencode-go", model); err != nil {
			t.Error(err)
		}
	}
	for _, model := range SupportedModelsForProvider("opencode-go") {
		if err := ValidateProviderModelType("opencode-go", model); err != nil {
			t.Error(err)
		}
		if ContextWindowForModel("opencode-go", model) != 1000000 {
			t.Errorf("incorrect context window for %s", model)
		}
		base, _ := openCodeGoModelEffort(model)
		wantVision := base == "glm-5.3-flash" || base == "deepseek-v4.1-flash" || base == "claude-haiku-5-5" || base == "qwen3.8-flash" || base == "step-5-preview-free"
		if SupportsVision("opencode-go", model) != wantVision || SupportsAudio("opencode-go", model) || SupportsPDF("opencode-go", model) {
			t.Errorf("incorrect media capabilities for %s", model)
		}
	}
	for _, model := range []string{"minimax-m3", "gpt-6-luna", "deepseek-v4.1-flash[none]", "deepseek-v4-pro[low]", "qwen3.8-flash[high]", "qwen3.8-flash[max]", "step-5-preview-free[none]", "step-5-preview-free[max]", "step-5-preview"} {
		if ValidateProviderModelType("opencode-go", model) == nil {
			t.Errorf("unsupported model %s was accepted", model)
		}
	}
}

func TestOpenCodeGoRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"MissingSessionID","type":"invalid_request_error"}}`)
	}))
	defer server.Close()
	p := newOpenCodeGoProvider("key", server.URL, "glm-5.3", "", 100)
	result, err := p.Chat(context.Background(), &Request{Messages: []Message{{Role: "user", Content: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := result.Wait(); err == nil || !strings.Contains(err.Error(), "MissingSessionID") {
		t.Fatalf("gateway error was lost: %v", err)
	}
}

func TestOpenCodeGoSSERejectsInvalidPayload(t *testing.T) {
	for _, test := range []struct{ name, payload, want string }{
		{"truncated-json", `{"choices":`, "unexpected end of JSON input"},
		{"upstream-error", `{"error":{"message":"upstream failed"}}`, "upstream failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				fmt.Fprintf(w, ": keep-alive\n\ndata: %s\n\n", test.payload)
			}))
			defer server.Close()
			p := newOpenCodeGoProvider("key", server.URL, "glm-5.3-flash", "", 100)
			result, err := p.Chat(context.Background(), &Request{Messages: []Message{{Role: "user", Content: "test"}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := result.Wait(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid payload was not reported: %v", err)
			}
		})
	}
}

func TestOpenCodeGoCancelDuringKeepAlive(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := newOpenCodeGoProvider("key", server.URL, "glm-5.3-flash", "", 100)
	result, err := p.Chat(ctx, &Request{Messages: []Message{{Role: "user", Content: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	result.(StreamChatResult).Cancel()
	if _, err := result.Wait(); err == nil {
		t.Fatal("cancelled keep-alive stream completed without an error")
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("Cancel did not stop the keep-alive request")
	}
}

func TestOpenCodeGoCancelStopsRequest(t *testing.T) {
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"start\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := newOpenCodeGoProvider("key", server.URL, "glm-5.3", "", 100)
	result, err := p.Chat(ctx, &Request{Messages: []Message{{Role: "user", Content: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	stream := result.(StreamChatResult)
	if delta, err := stream.Recv(); err != nil || delta.Text != "start" {
		t.Fatalf("missing first streamed token: %+v, %v", delta, err)
	}
	stream.Cancel()
	if _, err := stream.Wait(); err == nil {
		t.Fatal("cancelled request completed without an error")
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("Cancel did not stop the HTTP request")
	}
}

func openCodeGoTestSessionID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}
