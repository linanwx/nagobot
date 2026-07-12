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

// wsPoolTestServer keeps every accepted connection alive and draining, so the
// pool's idle loop sees a healthy, pong-answering peer (gorilla's default
// ping handler answers pings only while a read is pending — hence the loop).
func wsPoolTestServer(t *testing.T) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialPoolWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func shrinkPoolClocks(t *testing.T, ttl, ping time.Duration) {
	t.Helper()
	origTTL, origPing := wsPoolIdleTTL, wsPingInterval
	wsPoolIdleTTL, wsPingInterval = ttl, ping
	t.Cleanup(func() {
		wsPoolIdleTTL, wsPingInterval = origTTL, origPing
	})
}

func TestWSPoolTakePutRoundTrip(t *testing.T) {
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	conn := dialPoolWS(t, url)
	pool.Put("s1|gpt-5.5", &wsPoolEntry{
		conn:             conn,
		lastResponseID:   "resp_42",
		lastInputItems:   []map[string]any{{"type": "message", "role": "user"}},
		lastInstructions: "sys prompt",
		lastToolsFP:      "fp123",
		baseURL:          "https://chatgpt.com/backend-api/codex",
		accountID:        "acct-1",
	})

	got := pool.Take("s1|gpt-5.5", "https://chatgpt.com/backend-api/codex", "acct-1")
	if got == nil {
		t.Fatal("expected pooled entry back")
	}
	defer got.conn.Close()
	if got.conn != conn {
		t.Fatal("expected the same connection back")
	}
	if got.lastResponseID != "resp_42" || got.lastInstructions != "sys prompt" || got.lastToolsFP != "fp123" {
		t.Fatalf("continuation state did not survive the round trip: %+v", got)
	}
	if len(got.lastInputItems) != 1 {
		t.Fatalf("lastInputItems did not survive: %+v", got.lastInputItems)
	}

	if again := pool.Take("s1|gpt-5.5", "https://chatgpt.com/backend-api/codex", "acct-1"); again != nil {
		t.Fatal("Take must remove the entry — second Take should miss")
	}
}

func TestWSPoolKeyIsolation(t *testing.T) {
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	c1, c2 := dialPoolWS(t, url), dialPoolWS(t, url)
	pool.Put("s1|m", &wsPoolEntry{conn: c1, lastResponseID: "r1", baseURL: "b", accountID: "a"})
	pool.Put("s2|m", &wsPoolEntry{conn: c2, lastResponseID: "r2", baseURL: "b", accountID: "a"})

	got := pool.Take("s1|m", "b", "a")
	if got == nil || got.lastResponseID != "r1" {
		t.Fatalf("wrong entry for s1|m: %+v", got)
	}
	got.conn.Close()
	got2 := pool.Take("s2|m", "b", "a")
	if got2 == nil || got2.lastResponseID != "r2" {
		t.Fatalf("wrong entry for s2|m: %+v", got2)
	}
	got2.conn.Close()
}

func TestWSPoolIdentityMismatchDiscards(t *testing.T) {
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	pool.Put("k", &wsPoolEntry{conn: dialPoolWS(t, url), baseURL: "old-base", accountID: "acct-1"})
	if got := pool.Take("k", "new-base", "acct-1"); got != nil {
		t.Fatal("baseURL mismatch must not reuse the connection")
	}

	pool.Put("k", &wsPoolEntry{conn: dialPoolWS(t, url), baseURL: "b", accountID: "acct-1"})
	if got := pool.Take("k", "b", "acct-2"); got != nil {
		t.Fatal("accountID mismatch must not reuse the connection")
	}
}

func TestWSPoolIdleTTLExpires(t *testing.T) {
	shrinkPoolClocks(t, 60*time.Millisecond, 15*time.Millisecond)
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	pool.Put("k", &wsPoolEntry{conn: dialPoolWS(t, url), baseURL: "b", accountID: "a"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pool.mu.Lock()
		n := len(pool.entries)
		pool.mu.Unlock()
		if n == 0 {
			return // expired and self-removed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("entry did not expire after wsPoolIdleTTL")
}

func TestWSPoolSurvivesIdleWithKeepalive(t *testing.T) {
	// Long TTL, fast pings: the loop must ride many ping cycles on a healthy
	// peer without killing the connection.
	shrinkPoolClocks(t, 10*time.Second, 15*time.Millisecond)
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	pool.Put("k", &wsPoolEntry{conn: dialPoolWS(t, url), baseURL: "b", accountID: "a"})
	time.Sleep(300 * time.Millisecond) // ~20 ping cycles
	got := pool.Take("k", "b", "a")
	if got == nil {
		t.Fatal("healthy idle connection was dropped")
	}
	got.conn.Close()
}

func TestWSPoolDeadConnectionRemoved(t *testing.T) {
	// Detection is bound to the keepalive ping: a dead socket fails its next
	// WriteControl. (A remote close is NOT detected while parked — no reader —
	// and surfaces on adoption instead, where chatViaWS redials once.)
	shrinkPoolClocks(t, 10*time.Second, 15*time.Millisecond)
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	conn := dialPoolWS(t, url)
	pool.Put("k", &wsPoolEntry{conn: conn, baseURL: "b", accountID: "a"})
	conn.Close() // socket death while parked

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pool.mu.Lock()
		n := len(pool.entries)
		pool.mu.Unlock()
		if n == 0 {
			if got := pool.Take("k", "b", "a"); got != nil {
				t.Fatal("Take returned a connection known to be dead")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dead entry was not removed from the pool")
}

func TestWSPoolLRUEviction(t *testing.T) {
	origMax := wsPoolMaxEntries
	wsPoolMaxEntries = 2
	t.Cleanup(func() { wsPoolMaxEntries = origMax })

	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	pool.Put("k1", &wsPoolEntry{conn: dialPoolWS(t, url), baseURL: "b", accountID: "a"})
	time.Sleep(5 * time.Millisecond)
	pool.Put("k2", &wsPoolEntry{conn: dialPoolWS(t, url), baseURL: "b", accountID: "a"})
	time.Sleep(5 * time.Millisecond)
	pool.Put("k3", &wsPoolEntry{conn: dialPoolWS(t, url), baseURL: "b", accountID: "a"})

	if got := pool.Take("k1", "b", "a"); got != nil {
		t.Fatal("oldest entry should have been evicted")
	}
	for _, k := range []string{"k2", "k3"} {
		got := pool.Take(k, "b", "a")
		if got == nil {
			t.Fatalf("entry %s should have survived eviction", k)
		}
		got.conn.Close()
	}
}

// TestProviderCloseParksAndAdopts exercises the provider-side halves: a
// healthy turn's Close() parks connection+continuation, and the next turn's
// ensureWSConn adopts both without dialing.
func TestProviderCloseParksAndAdopts(t *testing.T) {
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	p1 := newTestOAuthProvider()
	p1.pool = pool
	p1.poolKey = "sess|gpt-5.5"
	p1.wsConn = dialPoolWS(t, url)
	p1.lastResponseID = "resp_9"
	p1.lastInputItems = []map[string]any{{"type": "message"}}
	p1.lastInstructions = "sys"
	p1.lastToolsFP = "fp"
	p1.Close()

	if p1.wsConn != nil {
		t.Fatal("Close must release the provider's reference to the connection")
	}

	p2 := newTestOAuthProvider()
	p2.pool = pool
	p2.poolKey = "sess|gpt-5.5"
	conn, err := p2.ensureWSConn(context.Background())
	if err != nil {
		t.Fatalf("ensureWSConn: %v", err)
	}
	defer p2.closeHard()
	if conn == nil || !p2.wsFromPool {
		t.Fatal("expected adoption from pool, not a fresh dial")
	}
	if p2.lastResponseID != "resp_9" || p2.lastInstructions != "sys" || p2.lastToolsFP != "fp" || len(p2.lastInputItems) != 1 {
		t.Fatalf("continuation state was not adopted with the connection: %+v", p2)
	}
}

// TestProviderCloseAfterFailureNeverParks: wsFailed turns must tear down, not
// park — parking a connection that just failed would poison the next turn.
func TestProviderCloseAfterFailureNeverParks(t *testing.T) {
	url := wsPoolTestServer(t)
	pool := NewWSPool()
	t.Cleanup(pool.CloseAll)

	p := newTestOAuthProvider()
	p.pool = pool
	p.poolKey = "sess|gpt-5.5"
	p.wsConn = dialPoolWS(t, url)
	p.wsFailed = true
	p.Close()

	if got := pool.Take("sess|gpt-5.5", p.baseURL, p.accountID); got != nil {
		t.Fatal("a failed turn's connection must not be parked")
	}
}
