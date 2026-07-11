package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsTestServer accepts one WebSocket connection, reads the request frame, and
// then hands the connection to onRequest. Returns the ws:// URL.
func wsTestServer(t *testing.T, onRequest func(conn *websocket.Conn)) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		onRequest(conn)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]any{"type": "response.create"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	return conn
}

// shrinkWSClocks makes the liveness timeouts test-sized.
func shrinkWSClocks(t *testing.T, stall, ping, pong time.Duration) {
	t.Helper()
	origStall, origPing, origPong := wsStallTimeout, wsPingInterval, wsPongTimeout
	wsStallTimeout, wsPingInterval, wsPongTimeout = stall, ping, pong
	t.Cleanup(func() {
		wsStallTimeout, wsPingInterval, wsPongTimeout = origStall, origPing, origPong
	})
}

// This is the bug, reproduced. On 2026-07-11 the Codex backend accepted a
// request on a healthy socket and then said nothing — no frame, no error, no
// close — and the read blocked for as long as the process lived. The turn never
// ended and the thread stayed wedged; the session's next message queued behind a
// reply that was never coming. Nothing failed, which is precisely why nothing
// recovered.
//
// The server here does exactly that: it accepts, it reads, it goes quiet. It
// even answers pings, so the connection is alive by every transport-level
// measure — the case a pong-extended read deadline would sleep through forever.
// parseWSStream must still give up.
func TestParseWSStream_SilentServerIsNotWaitedOnForever(t *testing.T) {
	shrinkWSClocks(t, 300*time.Millisecond, 50*time.Millisecond, time.Second)

	hold := make(chan struct{})
	url := wsTestServer(t, func(conn *websocket.Conn) {
		<-hold // accept the request, then never speak again
	})
	defer close(hold)

	conn := dialWS(t, url)
	p := &OpenAIProvider{}
	resp := &Response{}
	adapter := newStreamAdapter(context.Background(), resp)

	done := make(chan error, 1)
	go func() {
		_, emitted, err := p.parseWSStream(context.Background(), conn, adapter)
		if emitted {
			t.Errorf("nothing was ever sent, so nothing can have been emitted")
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a server that never speaks must produce an error, not a response")
		}
		// The error has to reach chatViaWS as an ordinary stream failure — that
		// is what marks the transport failed and retries over HTTP. A hang has
		// no error to carry, which is why it had no recovery.
		if !strings.Contains(err.Error(), "reading websocket stream") {
			t.Errorf("want a read failure the caller already knows how to recover from, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parseWSStream is still blocked in ReadMessage — the stall deadline did not fire")
	}
}

// The stall deadline measures silence, not total duration: a server that keeps
// speaking may take as long as it likes. Without this the fix would trade a hang
// for a truncated long answer, which is a worse bug than the one it replaces.
func TestParseWSStream_SlowButTalkingServerIsNotCutOff(t *testing.T) {
	shrinkWSClocks(t, 200*time.Millisecond, 50*time.Millisecond, time.Second)

	url := wsTestServer(t, func(conn *websocket.Conn) {
		// Ten deltas at 100ms — 1s in total, five stall timeouts long, but never
		// 200ms quiet.
		for i := 0; i < 10; i++ {
			time.Sleep(100 * time.Millisecond)
			if err := conn.WriteJSON(map[string]any{
				"type": "response.output_text.delta", "delta": "x",
			}); err != nil {
				return
			}
		}
		_ = conn.WriteJSON(map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"id": "resp_slow"},
		})
	})

	conn := dialWS(t, url)
	p := &OpenAIProvider{}
	resp := &Response{}
	adapter := newStreamAdapter(context.Background(), resp)

	id, emitted, err := p.parseWSStream(context.Background(), conn, adapter)
	if err != nil {
		t.Fatalf("a server that keeps talking must not be cut off: %v", err)
	}
	if id != "resp_slow" || !emitted {
		t.Fatalf("stream did not assemble: id=%q emitted=%v", id, emitted)
	}
}

// The Codex backend only honors previous_response_id inside the WS session that
// produced it, so the connection is reused across the turn's tool-call
// iterations. The runner now cancels its per-call context the moment a response
// completes — and that context is the one parseWSStream's watcher goroutine
// closes the connection on. If the watcher lost that race it would tear down a
// perfectly good socket between iterations, silently demoting the rest of the
// turn to full-context HTTP. A finished stream must win the tie.
func TestParseWSStream_CallerCancelAfterCompletionKeepsConnAlive(t *testing.T) {
	url := wsTestServer(t, func(conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"id": "resp_1"},
		})
		// Stay up: the next iteration would reuse this connection.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	conn := dialWS(t, url)
	p := &OpenAIProvider{}
	resp := &Response{}
	ctx, cancel := context.WithCancel(context.Background())
	adapter := newStreamAdapter(ctx, resp)

	id, _, err := p.parseWSStream(ctx, conn, adapter)
	if err != nil {
		t.Fatalf("parseWSStream: %v", err)
	}
	if id != "resp_1" {
		t.Fatalf("responseID = %q, want resp_1", id)
	}

	cancel() // exactly what Runner.callLLM's deferred cancel does
	time.Sleep(100 * time.Millisecond)

	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(map[string]any{"type": "response.create"}); err != nil {
		t.Fatalf("the connection was closed by the cancel, so the next iteration lost its continuation: %v", err)
	}
}
