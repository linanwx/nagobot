package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/linanwx/nagobot/logger"
)

const (
	openAIAPIBase      = "https://api.openai.com/v1"
	openAIChatGPTBase  = "https://chatgpt.com/backend-api/codex"
	openAIChatGPTWSURL = "wss://chatgpt.com/backend-api/codex/responses"
	// openAIBetaWebSockets unlocks previous_response_id continuation on the
	// ChatGPT/Codex backend. That backend rejects the parameter entirely over
	// plain HTTP (400 "Unsupported parameter: previous_response_id" —
	// confirmed empirically against the real API), and only honors it inside
	// a WebSocket connection carrying this header — confirmed by reading
	// OpenClaw's implementation, which sends previous_response_id exclusively
	// on its WebSocket code path and never on its plain HTTP/SSE path.
	openAIBetaWebSockets = "responses_websockets=2026-02-06"

	// wsWriteTimeout bounds a control frame (a ping — a few bytes);
	// wsRequestWriteTimeout bounds the request body, which is routinely
	// hundreds of kilobytes of context.
	wsWriteTimeout        = 10 * time.Second
	wsRequestWriteTimeout = time.Minute
)

// The WebSocket liveness clocks. Vars rather than consts so the stall test can
// run in milliseconds instead of minutes; nothing outside tests writes them.
var (
	// wsStallTimeout is how long the Codex backend may go completely silent —
	// not one frame of any kind — before the request is declared dead. It is a
	// silence budget, not a latency budget: a response that streams for ten
	// minutes never approaches it, while a healthy turn's time-to-first-event
	// is seconds (the backend emits response.created on acceptance). Two
	// minutes is generous against that and still bounded, and a false trip
	// costs one HTTP retry rather than a failed turn.
	wsStallTimeout = 2 * time.Minute
	// wsPingInterval / wsPongTimeout catch the other death: a peer that is gone
	// but never sent an RST. Two missed pongs is the signal — and only for a
	// peer that has ponged at least once (see parseWSStream).
	wsPingInterval = 20 * time.Second
	wsPongTimeout  = 50 * time.Second
)

func init() {
	models := []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano"}
	constructor := func(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) Provider {
		return newOpenAIProvider(apiKey, apiBase, modelType, modelName, maxTokens, temperature)
	}

	// "openai" — API key auth, hits api.openai.com directly. Context windows
	// reflect the model's full API capacity.
	RegisterProvider("openai", ProviderRegistration{
		Models:       models,
		VisionModels: models,
		ContextWindows: map[string]int{
			"gpt-5.6-sol":   372000,
			"gpt-5.6-terra": 372000,
			"gpt-5.6-luna":  372000,
			"gpt-5.5":       1048576,
			"gpt-5.4":       1048576,
			"gpt-5.4-mini":  400000,
			"gpt-5.4-nano":  200000,
		},
		EnvKey:      "OPENAI_API_KEY",
		EnvBase:     "OPENAI_API_BASE",
		Constructor: constructor,
	})

	// "openai-oauth" — OAuth token auth via the ChatGPT codex backend
	// (chatgpt.com/backend-api/codex). Context limits are sourced from
	// GET /backend-api/codex/models?client_version=1.0.0.
	RegisterProvider("openai-oauth", ProviderRegistration{
		Models:       models,
		VisionModels: models,
		ContextWindows: map[string]int{
			"gpt-5.6-sol":   372000,
			"gpt-5.6-terra": 372000,
			"gpt-5.6-luna":  372000,
			"gpt-5.5":       272000,
			"gpt-5.4":       272000,
			"gpt-5.4-mini":  272000,
			"gpt-5.4-nano":  272000,
		},
		Constructor: constructor,
	})
}

// OpenAIProvider implements the Provider interface using the OpenAI Responses API.
type OpenAIProvider struct {
	apiKey      string
	baseURL     string
	modelName   string
	modelType   string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
	accountID   string // ChatGPT account ID from OAuth id_token

	// Continuation state for previous_response_id chaining. Scoped to this
	// provider instance's lifetime, which is exactly one turn's agentic loop
	// (ProviderFactory.Create() builds a fresh instance per turn — see
	// thread.resolveProvider). Only reachable on the OAuth/ChatGPT-Codex
	// backend (accountID != "") and only actually usable over the WebSocket
	// transport below — see wsFailed. lastInputItems is the full logical
	// input array (in Responses API item form) as of the last successful
	// call, plus a synthesized echo of that call's own assistant reply — i.e.
	// everything the server already knows as of lastResponseID.
	lastResponseID string
	lastInputItems []map[string]any

	// WebSocket transport state (OAuth/ChatGPT-Codex backend only). One
	// connection is dialed lazily on first use and reused for every
	// subsequent tool-call iteration within this provider instance's turn,
	// matching the Codex backend's requirement that previous_response_id
	// only works inside the WS session that produced it. wsFailed is sticky:
	// once the WS transport fails for any reason (dial, write, or a stream
	// error before anything was emitted), the rest of the turn falls back to
	// plain HTTP (full context, no continuation) instead of repeatedly
	// retrying a bad connection or network path.
	// wsRequestID is sent as both session_id and x-client-request-id on the
	// WS handshake. Derived from the session key (stable across turns) when
	// ctx carries one, random otherwise — see ensureWSConn.
	wsConn      *websocket.Conn
	wsFailed    bool
	wsRequestID string
	wsQuota     *Quota
}

// invalidateContinuation drops any previous_response_id chain, forcing the
// next call to send full context. Called after any failure so a bad or
// expired response id can't be reused.
func (p *OpenAIProvider) invalidateContinuation() {
	p.lastResponseID = ""
	p.lastInputItems = nil
}

// updateContinuation records the continuation state after a successful
// response, so the next call on this provider instance (next tool-call
// iteration within the same turn) can send only the delta.
func (p *OpenAIProvider) updateContinuation(fullItems []map[string]any, responseID string, resp *Response) {
	if responseID == "" {
		p.invalidateContinuation()
		return
	}
	echo := assistantMessageToInputItems(Message{
		Content:          resp.Content,
		ReasoningDetails: resp.ReasoningDetails,
		ToolCalls:        resp.ToolCalls,
	})
	baseline := make([]map[string]any, 0, len(fullItems)+len(echo))
	baseline = append(baseline, fullItems...)
	baseline = append(baseline, echo...)
	p.lastResponseID = responseID
	p.lastInputItems = baseline
}

// SetAccountID sets the ChatGPT account ID for OAuth-based requests.
func (p *OpenAIProvider) SetAccountID(id string) {
	p.accountID = id
}

// Close releases the WebSocket connection, if one is open. Implements the
// optional provider.Closer interface — thread.executeRunner defer-calls this
// once the turn's Runner loop ends, mirroring the AccountIDSetter pattern for
// optional per-provider capabilities. Safe to call multiple times.
func (p *OpenAIProvider) Close() {
	if p.wsConn != nil {
		p.wsConn.Close()
		p.wsConn = nil
	}
}

func newOpenAIProvider(apiKey, apiBase, modelType, modelName string, maxTokens int, temperature float64) *OpenAIProvider {
	if modelName == "" {
		modelName = modelType
	}
	baseURL := strings.TrimSpace(apiBase)
	if baseURL == "" {
		baseURL = openAIAPIBase
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &OpenAIProvider{
		apiKey:      apiKey,
		baseURL:     baseURL,
		modelName:   modelName,
		modelType:   modelType,
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// Chat sends a chat completion request. On the OAuth/ChatGPT-Codex backend it
// prefers a persistent WebSocket connection — required for
// previous_response_id delta continuation, see wsFailed's doc comment above.
// Any other provider, or any WebSocket failure, uses plain HTTP with full
// context.
func (p *OpenAIProvider) Chat(ctx context.Context, req *Request) (ChatResult, error) {
	if p.accountID != "" && !p.wsFailed {
		return p.chatViaWS(ctx, req)
	}
	return p.chatViaHTTP(ctx, req)
}

// chatViaWS sends the request over the Codex backend's WebSocket transport,
// reusing this provider instance's connection across calls so
// previous_response_id chaining is valid. Any failure before the dial or the
// initial send falls back to chatViaHTTP synchronously; a stream failure that
// happens after the goroutine is already returning deltas to the caller
// cannot be silently retried (some output may already be visible), so it is
// only retried if nothing was emitted yet.
func (p *OpenAIProvider) chatViaWS(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	conn, err := p.ensureWSConn(ctx)
	if err != nil {
		logger.Warn("openai-oauth websocket dial failed, falling back to HTTP for the rest of this turn", "err", err)
		p.wsFailed = true
		p.invalidateContinuation()
		return p.chatViaHTTP(ctx, req)
	}

	built, err := p.buildRequestBody(ctx, req, false)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	logger.Info(
		"openai request",
		"provider", "openai-oauth",
		"transport", "ws",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
		"usedDelta", built.usedDelta,
	)

	wsMsg := make(map[string]any, len(built.bodyMap)+1)
	maps.Copy(wsMsg, built.bodyMap)
	wsMsg["type"] = "response.create"

	// The write needs its own deadline, not the caller's context: gorilla's
	// WriteJSON does not take one, so a peer that stops draining the socket
	// mid-request would block here with no way for a cancelled ctx to reach it.
	// Requests are large (hundreds of KB of context is normal) but they go to a
	// socket that was healthy seconds ago, so a minute is silence, not slowness.
	_ = conn.SetWriteDeadline(time.Now().Add(wsRequestWriteTimeout))
	if err := conn.WriteJSON(wsMsg); err != nil {
		logger.Warn("openai-oauth websocket write failed, falling back to HTTP for the rest of this turn", "err", err)
		p.Close()
		p.wsFailed = true
		p.invalidateContinuation()
		return p.chatViaHTTP(ctx, req)
	}

	resp := &Response{ProviderLabel: "openai-oauth", ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()

		responseID, emitted, perr := p.parseWSStream(ctx, conn, adapter)
		if perr != nil {
			p.Close()
			p.wsFailed = true
			p.invalidateContinuation()
			if !emitted {
				logger.Warn("openai-oauth websocket stream failed before any output, retrying with full context over HTTP",
					"err", perr)
				p.runHTTPStreamInto(ctx, req, adapter, resp, "openai-oauth", start)
				return
			}
			logger.Error("openai-oauth websocket stream error", "err", perr)
			adapter.SetError(perr)
			return
		}

		p.updateContinuation(built.fullItems, responseID, resp)
		if p.wsQuota != nil {
			resp.Quota = p.wsQuota
		}

		logger.Info(
			"openai response",
			"provider", resp.ProviderLabel,
			"transport", "ws",
			"modelType", p.modelType,
			"modelName", p.modelName,
			"hasToolCalls", len(resp.ToolCalls) > 0,
			"toolCallCount", len(resp.ToolCalls),
			"promptTokens", resp.Usage.PromptTokens,
			"completionTokens", resp.Usage.CompletionTokens,
			"reasoningTokens", resp.Usage.ReasoningTokens,
			"cachedTokens", resp.Usage.CachedTokens,
			"totalTokens", resp.Usage.TotalTokens,
			"outputChars", len(resp.Content),
			"usedDelta", built.usedDelta,
			"latencyMs", time.Since(start).Milliseconds(),
		)
	}()

	return adapter.Result(), nil
}

// ensureWSConn returns this provider's WebSocket connection to the Codex
// backend, dialing one on first use. The connection stays open for the
// lifetime of this provider instance (one turn) and is reused across every
// tool-call iteration within it, since previous_response_id continuation is
// scoped to the WS session that produced the referenced response.
func (p *OpenAIProvider) ensureWSConn(ctx context.Context) (*websocket.Conn, error) {
	if p.wsConn != nil {
		return p.wsConn, nil
	}
	if p.wsRequestID == "" {
		// Prefer a per-session stable id over a random one: the backend uses
		// session_id for cache-affine routing, so a value that survives across
		// turns lets each new turn's first full-context call land on the shard
		// that still holds the previous turn's prompt cache.
		p.wsRequestID = sessionCacheKey(ctx)
		if p.wsRequestID == "" {
			p.wsRequestID = randomHex(16)
		}
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.apiKey)
	header.Set("ChatGPT-Account-ID", p.accountID)
	header.Set("originator", "nagobot")
	header.Set("User-Agent", "nagobot/1.0")
	header.Set("OpenAI-Beta", openAIBetaWebSockets)
	header.Set("x-client-request-id", p.wsRequestID)
	header.Set("session_id", p.wsRequestID)

	conn, httpResp, err := websocket.DefaultDialer.DialContext(ctx, openAIChatGPTWSURL, header)
	if err != nil {
		return nil, err
	}
	if httpResp != nil {
		p.wsQuota = extractQuota(httpResp.Header)
	}
	p.wsConn = conn
	return conn, nil
}

// parseWSStream reads WebSocket text frames from an already-open Codex
// connection until one full response completes, assembling it the same way
// parseSSEStream does over HTTP — same event vocabulary, different framing
// (one JSON object per WS message instead of SSE "data:" lines). Unlike the
// HTTP path, the connection is NOT closed after one response — it is reused
// for the next call — so this loop must stop explicitly on a completion
// event rather than relying on EOF. A watcher goroutine closes the
// connection if ctx is cancelled, since gorilla/websocket reads don't
// otherwise respect context cancellation.
//
// It also has to survive a server that says nothing at all. On 2026-07-11 the
// Codex backend accepted a request on a freshly dialed socket, wrote no frame,
// and did not close: the read below blocked, the turn never ended, and the
// thread stayed wedged until the daemon was restarted — no error, no timeout,
// no reply. The failure was invisible precisely because nothing failed. Two
// independent clocks now bound that silence:
//
//   - wsStallTimeout, a read deadline refreshed on every frame. It measures
//     silence in RESPONSE EVENTS, which is why the pong handler deliberately
//     does NOT extend it. A socket that dutifully answers pings while its
//     backend has forgotten the request is exactly the case that hung us, and
//     a pong-extended deadline would have slept through it.
//   - wsPingInterval, a keepalive ping whose missing pong reveals a peer that
//     has gone away without an RST — the ordinary NAT/proxy death, which the
//     stall deadline would otherwise take two minutes to notice.
//
// Either one trips a read error, which is a language the caller already speaks:
// chatViaWS marks the transport failed and, since nothing was emitted, retries
// the whole request over HTTP with full context. The recovery path was always
// there. It just had no way to be reached.
func (p *OpenAIProvider) parseWSStream(ctx context.Context, conn *websocket.Conn, adapter *streamAdapter) (responseID string, emitted bool, err error) {
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			// Both channels can be ready at once: the caller cancels its
			// per-call context right after the response completes, and this
			// goroutine may not have been scheduled until then. Closing the
			// connection there would destroy a healthy socket the next
			// tool-call iteration is about to reuse for previous_response_id
			// continuation, so a finished stream always wins the tie.
			select {
			case <-watchDone:
			default:
				conn.Close()
			}
		case <-watchDone:
		}
	}()

	// A pong proves the peer is alive; its ABSENCE only proves something once we
	// know this peer answers pings at all. RFC 6455 makes a pong mandatory, but
	// betting the working transport on a remote server's RFC compliance is a bad
	// trade — if chatgpt.com ever declined to pong, enforcing the timeout would
	// tear down healthy connections mid-response to defend against a dead-peer
	// case we have never actually seen. So enforcement arms itself on the first
	// pong received. Until then the stall deadline above is the only guard, which
	// is exactly the guard the observed failure needed.
	var lastPong atomic.Int64
	conn.SetPongHandler(func(string) error {
		lastPong.Store(time.Now().UnixNano())
		return nil
	})
	go func() {
		t := time.NewTicker(wsPingInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				// WriteControl is the one write method gorilla documents as safe
				// to call concurrently with the request write on another goroutine.
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
					return // the read side will see the same broken socket
				}
				seen := lastPong.Load()
				if seen == 0 {
					continue // this peer has never ponged; not our business to judge it
				}
				if since := time.Since(time.Unix(0, seen)); since > wsPongTimeout {
					logger.Warn("openai-oauth websocket peer stopped answering pings, closing",
						"sincePong", since.Round(time.Second))
					conn.Close()
					return
				}
			case <-watchDone:
				return
			}
		}
	}()

	asm := newResponseAssembler(adapter)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(wsStallTimeout)); err != nil {
			return "", asm.emitted, fmt.Errorf("setting websocket read deadline: %w", err)
		}
		_, data, readErr := conn.ReadMessage()
		if readErr != nil {
			return "", asm.emitted, fmt.Errorf("reading websocket stream: %w", readErr)
		}
		done, evErr := asm.handleEvent(data)
		if evErr != nil {
			return "", asm.emitted, evErr
		}
		if done {
			break
		}
	}
	asm.finish()
	return asm.responseID, asm.emitted, nil
}

// chatViaHTTP sends one full-context request over plain HTTP/SSE. Used for
// the non-OAuth (api.openai.com, API-key) path always, and as the sticky
// fallback for openai-oauth once the WebSocket transport has failed for this
// turn — the Codex backend never accepts previous_response_id outside a WS
// session, so this path always sends full context.
func (p *OpenAIProvider) chatViaHTTP(ctx context.Context, req *Request) (ChatResult, error) {
	start := time.Now()
	inputChars := inputChars(req.Messages)

	providerLabel := "openai"
	if p.accountID != "" {
		providerLabel = "openai-oauth"
	}

	logger.Info(
		"openai request",
		"provider", providerLabel,
		"transport", "http",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"toolCount", len(req.Tools),
		"inputChars", inputChars,
	)

	resp := &Response{ProviderLabel: providerLabel, ModelLabel: p.modelName}
	adapter := newStreamAdapter(ctx, resp)

	go func() {
		defer adapter.Finish()
		p.runHTTPStreamInto(ctx, req, adapter, resp, providerLabel, start)
	}()

	return adapter.Result(), nil
}

// runHTTPStreamInto performs one full-context HTTP request/SSE cycle,
// feeding results into an already-returned adapter. Used both as
// chatViaHTTP's own goroutine body, and inline (same goroutine, no nested
// adapter/Finish) as the WS-failure fallback in chatViaWS — so a broken
// WebSocket never fails a turn outright as long as nothing was streamed to
// the caller yet.
func (p *OpenAIProvider) runHTTPStreamInto(ctx context.Context, req *Request, adapter *streamAdapter, resp *Response, providerLabel string, start time.Time) {
	built, err := p.buildRequestBody(ctx, req, true)
	if err != nil {
		adapter.SetError(fmt.Errorf("failed to build request: %w", err))
		return
	}
	marshaled, err := json.Marshal(built.bodyMap)
	if err != nil {
		adapter.SetError(fmt.Errorf("failed to encode request: %w", err))
		return
	}

	base := p.baseURL
	if p.accountID != "" {
		base = openAIChatGPTBase
	}
	url := base + "/responses"

	httpResp, err := p.doRequest(ctx, url, marshaled)
	if err != nil {
		logger.Error("openai request error", "provider", providerLabel, "err", err)
		adapter.SetError(fmt.Errorf("request failed: %w", err))
		return
	}
	if httpResp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		logger.Error("openai request error", "provider", providerLabel, "status", httpResp.StatusCode, "body", string(errBody))
		adapter.SetError(fmt.Errorf("request failed: %d %s", httpResp.StatusCode, string(errBody)))
		return
	}

	_, _, perr := p.parseSSEStream(httpResp, adapter)
	httpResp.Body.Close()
	if perr != nil {
		logger.Error("openai SSE parse error", "provider", providerLabel, "err", perr)
		adapter.SetError(perr)
		return
	}

	// HTTP never chains previous_response_id (the Codex backend rejects it
	// outside a WebSocket session) — nothing to record for the next call.

	if p.accountID != "" {
		resp.Quota = extractQuota(httpResp.Header)
	}

	logger.Info(
		"openai response",
		"provider", resp.ProviderLabel,
		"transport", "http",
		"modelType", p.modelType,
		"modelName", p.modelName,
		"hasToolCalls", len(resp.ToolCalls) > 0,
		"toolCallCount", len(resp.ToolCalls),
		"promptTokens", resp.Usage.PromptTokens,
		"completionTokens", resp.Usage.CompletionTokens,
		"reasoningTokens", resp.Usage.ReasoningTokens,
		"cachedTokens", resp.Usage.CachedTokens,
		"totalTokens", resp.Usage.TotalTokens,
		"outputChars", len(resp.Content),
		"latencyMs", time.Since(start).Milliseconds(),
	)
}

// doRequest issues one POST to the Responses API with the given pre-built body.
func (p *OpenAIProvider) doRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	if p.accountID != "" {
		httpReq.Header.Set("ChatGPT-Account-ID", p.accountID)
	}
	return p.httpClient.Do(httpReq)
}

// builtRequest is the result of buildRequestBody: the Responses API request
// body (as a map, marshaled by whichever transport sends it) plus the
// bookkeeping needed to maintain (or retry) previous_response_id chaining.
type builtRequest struct {
	bodyMap   map[string]any
	usedDelta bool             // true if body carries previous_response_id + partial input
	fullItems []map[string]any // full logical input array as of this call (pre-delta-slicing)
}

// userMessageToInputItems converts a single user-role message to Responses API
// input items (always exactly one "message" item).
func userMessageToInputItems(msg Message) []map[string]any {
	content := []map[string]any{
		{"type": "input_text", "text": msg.Content},
	}
	if len(msg.Media) > 0 {
		_, markers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
		for _, marker := range markers {
			if !strings.HasPrefix(marker.MimeType, "image/") {
				continue // OpenAI Responses API only supports image media
			}
			b64, err := ReadFileAsBase64(marker.FilePath)
			if err != nil {
				continue
			}
			content = append(content, map[string]any{
				"type":      "input_image",
				"image_url": "data:" + marker.MimeType + ";base64," + b64,
			})
		}
	}
	return []map[string]any{{
		"type":    "message",
		"role":    "user",
		"content": content,
	}}
}

// assistantMessageToInputItems converts a single assistant-role message to
// Responses API input items: an optional output_text message, reasoning items
// (unless trimmed), then one function_call item per tool call. Used both for
// full-history conversion and to synthesize the "echo" of a just-completed
// response so continuation bookkeeping matches exactly what a later replay of
// the same message would produce.
func assistantMessageToInputItems(msg Message) []map[string]any {
	var input []map[string]any
	if msg.Content != "" {
		input = append(input, map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": msg.Content},
			},
		})
	}
	// Insert OpenAI reasoning items before function_calls if not trimmed.
	// Only include items with type="reasoning" — ReasoningDetails from other
	// providers (Anthropic thinking blocks, Gemini thought_signature) have
	// different formats and must be skipped.
	if !msg.ReasoningTrimmed && len(msg.ReasoningDetails) > 0 {
		var items []json.RawMessage
		if err := json.Unmarshal(msg.ReasoningDetails, &items); err == nil {
			for _, raw := range items {
				var ri map[string]any
				if err := json.Unmarshal(raw, &ri); err == nil {
					if riType, _ := ri["type"].(string); riType == "reasoning" {
						input = append(input, ri)
					}
				}
			}
		}
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if !json.Valid([]byte(args)) {
			args = "{}"
		}
		input = append(input, map[string]any{
			"type":      "function_call",
			"call_id":   tc.ID,
			"name":      tc.Function.Name,
			"arguments": args,
		})
	}
	return input
}

// toolMessageToInputItems converts a single tool-result message to Responses
// API input items (always exactly one function_call_output item).
func toolMessageToInputItems(msg Message) []map[string]any {
	cleanedText, markers := ParseMediaMarkers(msg.Content)
	hasMedia := len(markers) > 0
	output := []map[string]any{
		{"type": "input_text", "text": cleanedText},
	}
	for _, marker := range markers {
		if !strings.HasPrefix(marker.MimeType, "image/") {
			continue // OpenAI only supports image media
		}
		b64, err := ReadFileAsBase64(marker.FilePath)
		if err != nil {
			continue
		}
		output = append(output, map[string]any{
			"type":      "input_image",
			"image_url": "data:" + marker.MimeType + ";base64," + b64,
		})
	}
	// Process explicit media attachments.
	if len(msg.Media) > 0 {
		_, mediaMarkers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
		for _, marker := range mediaMarkers {
			if !strings.HasPrefix(marker.MimeType, "image/") {
				continue // OpenAI Responses API only supports image media
			}
			b64, err := ReadFileAsBase64(marker.FilePath)
			if err != nil {
				continue
			}
			output = append(output, map[string]any{
				"type":      "input_image",
				"image_url": "data:" + marker.MimeType + ";base64," + b64,
			})
			hasMedia = true
		}
	}
	if hasMedia {
		return []map[string]any{{
			"type":    "function_call_output",
			"call_id": msg.ToolCallID,
			"output":  output,
		}}
	}
	return []map[string]any{{
		"type":    "function_call_output",
		"call_id": msg.ToolCallID,
		"output":  msg.Content,
	}}
}

// convertMessagesToInputItems converts a run of messages to Responses API
// input items. System messages are skipped (handled separately as
// instructions) — callers that need instructions must scan for them too.
func convertMessagesToInputItems(messages []Message) []map[string]any {
	var input []map[string]any
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			input = append(input, userMessageToInputItems(msg)...)
		case "assistant":
			input = append(input, assistantMessageToInputItems(msg)...)
		case "tool":
			input = append(input, toolMessageToInputItems(msg)...)
		}
	}
	return input
}

// buildRequestBody converts internal Request to Responses API JSON.
//
// On the OAuth/ChatGPT-Codex backend (accountID != ""), if this provider
// instance already completed an earlier call in the same turn (lastResponseID
// set) and the freshly-reconstructed input array still starts with exactly
// what was recorded as the server's known state (lastInputItems), only the
// new tail is sent along with previous_response_id — the server reconstructs
// the rest. Any mismatch (compression touched old history, first call, retry
// after rejection, or forceFullContext) falls back to sending everything, so
// correctness never depends on the delta path succeeding.
//
// ctx supplies the session key (via provider.WithSessionKey) used to derive
// prompt_cache_key — stable per session so full-context calls across turns
// route to the same server-side prompt cache shard.
func (p *OpenAIProvider) buildRequestBody(ctx context.Context, req *Request, forceFullContext bool) (*builtRequest, error) {
	// Extract system messages into instructions — recomputed fully every call
	// regardless of delta/full mode.
	var instructions []string
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			instructions = append(instructions, msg.Content)
		}
	}

	fullItems := convertMessagesToInputItems(req.Messages)

	input := fullItems
	usedDelta := false
	previousResponseID := ""
	if !forceFullContext && p.accountID != "" && p.lastResponseID != "" &&
		len(fullItems) >= len(p.lastInputItems) &&
		reflect.DeepEqual(fullItems[:len(p.lastInputItems)], p.lastInputItems) {
		input = fullItems[len(p.lastInputItems):]
		usedDelta = true
		previousResponseID = p.lastResponseID
	}

	// Convert tools to Responses API format (flat structure).
	var tools []map[string]any
	for _, t := range req.Tools {
		tool := map[string]any{
			"type":       "function",
			"name":       t.Function.Name,
			"parameters": t.Function.Parameters,
		}
		if t.Function.Description != "" {
			tool["description"] = t.Function.Description
		}
		tools = append(tools, tool)
	}

	// gpt-5.6-sol burns tokens too fast at high effort for everyday use.
	effort := "high"
	if p.modelName == "gpt-5.6-sol" {
		effort = "medium"
	}

	body := map[string]any{
		"model":   p.modelName,
		"input":   input,
		"stream":  true,
		"store":   false,
		"include": []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{
			"effort":  effort,
			"summary": "auto",
		},
	}
	if previousResponseID != "" {
		body["previous_response_id"] = previousResponseID
	}
	if cacheKey := sessionCacheKey(ctx); cacheKey != "" {
		body["prompt_cache_key"] = cacheKey
	}
	if p.modelName == "gpt-5.4" || p.modelName == "gpt-5.5" {
		body["text"] = map[string]any{"verbosity": "low"}
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n\n")
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	// ChatGPT backend does not support max_output_tokens or temperature.
	// Mini/nano models do not support temperature.
	noTemp := p.accountID != "" || p.modelName == "gpt-5.4-mini" || p.modelName == "gpt-5.4-nano"
	if p.accountID == "" {
		if p.maxTokens > 0 {
			body["max_output_tokens"] = p.maxTokens
		}
		if p.temperature != 0 && !noTemp {
			body["temperature"] = p.temperature
		}
	}

	return &builtRequest{bodyMap: body, usedDelta: usedDelta, fullItems: fullItems}, nil
}

// parseSSEStream reads an SSE event stream and assembles the complete response.
// It populates the adapter's Response directly and emits deltas via the
// adapter, using the same event-assembly logic as parseWSStream (see
// responseAssembler).
//
// Returns the response's own id (for previous_response_id chaining — only
// meaningful on the WS path, but harmless to compute here too) and whether
// anything was ever emitted to the adapter — the caller uses "emitted" to
// decide whether a failure is safe to silently retry (nothing has reached the
// user/session yet) or must be surfaced.
func (p *OpenAIProvider) parseSSEStream(httpResp *http.Response, adapter *streamAdapter) (responseID string, emitted bool, err error) {
	asm := newResponseAssembler(adapter)

	scanner := bufio.NewScanner(httpResp.Body)
	// Increase buffer for large events.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {json}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		done, evErr := asm.handleEvent([]byte(data))
		if evErr != nil {
			return "", asm.emitted, evErr
		}
		if done {
			break
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return "", asm.emitted, fmt.Errorf("reading SSE stream: %w", scanErr)
	}

	asm.finish()
	return asm.responseID, asm.emitted, nil
}

// responseAssembler processes the Responses API's streaming event vocabulary
// (response.output_text.delta, response.output_item.done,
// response.completed/done/incomplete, response.failed) — shared between the
// SSE (HTTP) and WebSocket transports, since both carry identical JSON event
// payloads and differ only in framing (SSE "data:" lines vs. one WS text
// frame per event).
type responseAssembler struct {
	adapter *streamAdapter
	resp    *Response

	content          strings.Builder
	reasoning        strings.Builder
	reasoningItems   []json.RawMessage
	toolCalls        []ToolCall
	toolCallSignaled bool
	emitted          bool
	responseID       string
}

func newResponseAssembler(adapter *streamAdapter) *responseAssembler {
	return &responseAssembler{adapter: adapter, resp: adapter.resp}
}

// handleEvent processes one decoded event payload. done=true means the
// response has completed (successfully, or with response.incomplete — still
// a valid result to process, not an error); err set means response.failed or
// another unrecoverable condition that must abort the turn.
func (a *responseAssembler) handleEvent(data []byte) (done bool, err error) {
	var event struct {
		Type     string         `json:"type"`
		Delta    string         `json:"delta,omitempty"`
		Item     map[string]any `json:"item,omitempty"`
		Response struct {
			ID    string `json:"id"`
			Usage struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				TotalTokens        int `json:"total_tokens"`
				InputTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokensDetails struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
			Error *responsesAPIError `json:"error,omitempty"`
		} `json:"response,omitempty"`
	}
	if unmarshalErr := json.Unmarshal(data, &event); unmarshalErr != nil {
		return false, nil // skip unparseable events
	}

	switch event.Type {
	case "response.output_text.delta":
		if event.Delta != "" {
			a.emitted = true
		}
		a.adapter.EmitText(event.Delta)

	case "response.output_item.done":
		if itemType, _ := event.Item["type"].(string); itemType == "function_call" {
			if !a.toolCallSignaled {
				a.toolCallSignaled = true
				a.emitted = true
				name, _ := event.Item["name"].(string)
				a.adapter.EmitToolCall(name)
			}
		}
		a.extractOutputItem(event.Item)

	case "response.completed", "response.done", "response.incomplete":
		a.responseID = event.Response.ID
		a.resp.Usage = Usage{
			PromptTokens:     event.Response.Usage.InputTokens,
			CompletionTokens: event.Response.Usage.OutputTokens,
			TotalTokens:      event.Response.Usage.TotalTokens,
			CachedTokens:     event.Response.Usage.InputTokensDetails.CachedTokens,
			ReasoningTokens:  event.Response.Usage.OutputTokensDetails.ReasoningTokens,
		}
		return true, nil

	case "response.failed":
		errInfo := event.Response.Error
		if errInfo != nil {
			return false, fmt.Errorf("API error [%s]: %s", errInfo.Code, errInfo.Message)
		}
		return false, fmt.Errorf("API returned response.failed")
	}
	return false, nil
}

// extractOutputItem processes a single completed output item from the stream.
func (a *responseAssembler) extractOutputItem(item map[string]any) {
	if item == nil {
		return
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		contentArr, _ := item["content"].([]any)
		for _, c := range contentArr {
			block, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType == "output_text" {
				if text, _ := block["text"].(string); text != "" {
					if a.content.Len() > 0 {
						a.content.WriteString("\n")
					}
					a.content.WriteString(text)
				}
			}
		}

	case "function_call":
		callID, _ := item["call_id"].(string)
		name, _ := item["name"].(string)
		args, _ := item["arguments"].(string)
		a.toolCalls = append(a.toolCalls, ToolCall{
			ID:   callID,
			Type: "function",
			Function: FunctionCall{
				Name:      name,
				Arguments: args,
			},
		})

	case "reasoning":
		// Extract summary text from reasoning item.
		if summaryArr, ok := item["summary"].([]any); ok {
			for _, s := range summaryArr {
				block, ok := s.(map[string]any)
				if !ok {
					continue
				}
				if blockType, _ := block["type"].(string); blockType == "summary_text" {
					if text, _ := block["text"].(string); text != "" {
						if a.reasoning.Len() > 0 {
							a.reasoning.WriteString("\n")
						}
						a.reasoning.WriteString(text)
					}
				}
			}
		}
		// Preserve the complete reasoning item as raw JSON for round-trip.
		if raw, err := json.Marshal(item); err == nil {
			a.reasoningItems = append(a.reasoningItems, json.RawMessage(raw))
		}
	}
}

// finish flushes the assembled content/reasoning/tool calls into the
// adapter's Response. Called once after the event loop ends successfully —
// NOT called on error paths, matching the original single-function behavior.
func (a *responseAssembler) finish() {
	if len(a.reasoningItems) > 0 {
		a.resp.ReasoningDetails, _ = json.Marshal(a.reasoningItems)
	}
	a.resp.Content = a.content.String()
	a.resp.ReasoningContent = a.reasoning.String()
	a.resp.ToolCalls = a.toolCalls
}

type responsesAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// extractQuota parses x-ratelimit-* headers into a Quota snapshot.
// Returns nil if no rate-limit headers are present.
func extractQuota(h http.Header) *Quota {
	lr := headerInt(h, "X-Ratelimit-Limit-Requests")
	lt := headerInt(h, "X-Ratelimit-Limit-Tokens")
	rr := headerInt(h, "X-Ratelimit-Remaining-Requests")
	rt := headerInt(h, "X-Ratelimit-Remaining-Tokens")
	if lr == 0 && lt == 0 && rr == 0 && rt == 0 {
		return nil
	}
	return &Quota{
		LimitRequests:     lr,
		LimitTokens:       lt,
		RemainingRequests: rr,
		RemainingTokens:   rt,
		ResetRequests:     h.Get("X-Ratelimit-Reset-Requests"),
		ResetTokens:       h.Get("X-Ratelimit-Reset-Tokens"),
		UpdatedAt:         time.Now(),
	}
}

func headerInt(h http.Header, key string) int {
	v := h.Get(key)
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

// sessionCacheKey derives a stable per-session identifier from the session
// key carried in ctx (set by thread.executeRunner via WithSessionKey). Used
// as the request's prompt_cache_key and the WS handshake's session_id /
// x-client-request-id: a value that is stable across turns keeps the backend
// routing this session's requests to the same prompt cache shard, so each new
// turn's first full-context call can hit the previous turn's cached prefix.
// (With the old per-turn random id and no prompt_cache_key, those calls
// almost always missed — 0–2% observed cache hit vs 95%+ on delta calls.)
// Hashing rather than sending the raw key keeps the value in the 32-char hex
// shape already proven against the backend and avoids shipping raw
// channel/user ids in headers. Returns "" when ctx carries no session key
// (e.g. standalone scripts), letting callers fall back to old behavior.
func sessionCacheKey(ctx context.Context) string {
	key := SessionKeyFromContext(ctx)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

// randomHex returns a random lowercase hex string of length n*2. Fallback
// client-request-id/session_id source when ctx carries no session key.
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
