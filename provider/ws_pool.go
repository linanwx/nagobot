package provider

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/linanwx/nagobot/logger"
)

// Vars rather than consts so tests can run the TTL in milliseconds; nothing
// outside tests writes them (same convention as the WS liveness clocks).
var (
	// wsPoolIdleTTL bounds how long a parked connection is kept. It covers the
	// gap buckets where cross-turn continuation measurably pays (94% vs 55%
	// cache hit under 5 minutes); past ~15 minutes Tier-1 compression has
	// usually rewritten history, which invalidates the continuation anyway, so
	// keeping the socket longer buys nothing.
	wsPoolIdleTTL = 15 * time.Minute

	// wsPoolMaxEntries matches the thread manager's concurrency cap: there can
	// never be more simultaneously active sessions than threads.
	wsPoolMaxEntries = 16
)

// wsPoolEntry is one parked WebSocket connection plus the continuation state
// that is only valid on that exact connection. The Codex backend scopes
// previous_response_id to the WS session that produced it, so the two live
// and die together: adopting the connection without the state (or the state
// without the connection) is never correct.
type wsPoolEntry struct {
	conn *websocket.Conn

	// Continuation state, verbatim from the provider instance that parked it.
	lastResponseID string
	lastInputItems []map[string]any

	// Request-attribute snapshot: cross-turn delta additionally requires that
	// instructions and the tool list are unchanged (mirroring codex's
	// responses_request_properties_match) — see buildRequestBody.
	lastInstructions string
	lastToolsFP      string

	// Last quota seen at dial time. Carried across turns so monitor keeps a
	// (possibly stale, at most wsPoolIdleTTL old) value instead of none.
	wsQuota *Quota

	// Identity guard: reuse requires the same backend identity. A config
	// reload that changes the account or base URL must not resurrect a
	// connection handshaken under the old one.
	baseURL   string
	accountID string

	lastUsed time.Time

	// Keepalive coordination. Whoever removes the entry from the pool map
	// owns its shutdown: close stopIdle, then (optionally) wait on idleDone.
	// dead is written by the keepalive goroutine before idleDone closes (the
	// channel close is the happens-before edge for readers).
	stopIdle chan struct{}
	idleDone chan struct{}
	dead     bool
}

// WSPool parks healthy Codex WebSocket connections between turns, keyed by
// session+model. Provider instances are turn-scoped (rebuilt every turn for
// config hot-reload), so without this pool every turn's first call redials
// and loses previous_response_id continuation — measured as the difference
// between a 55% and a 94% prompt-cache hit rate on same-gap calls.
//
// A parked connection has NO reader: gorilla/websocket read errors are
// permanent (even a deadline timeout poisons the read side for good), so an
// idle read loop could never hand the socket back. Keepalive is therefore
// client pings only (WriteControl is safe alongside no reader), which means
// two deaths go undetected while parked: a server that gives up on its own
// unanswered pings, and a silently dropped path. Both surface on adoption as
// a write or pre-emission stream failure, and chatViaWS redials exactly once
// for those — a stale adoption costs one redial, the same as a pool miss.
type WSPool struct {
	mu      sync.Mutex
	entries map[string]*wsPoolEntry
	closed  bool
}

func NewWSPool() *WSPool {
	return &WSPool{entries: make(map[string]*wsPoolEntry)}
}

// Take removes and returns the entry for key, or nil if there is none, its
// keepalive declared it dead, or its identity no longer matches. The caller
// owns the returned connection exclusively — the keepalive goroutine has
// exited by the time Take returns.
func (p *WSPool) Take(key, baseURL, accountID string) *wsPoolEntry {
	if p == nil || key == "" {
		return nil
	}
	p.mu.Lock()
	e := p.entries[key]
	if e != nil {
		delete(p.entries, key)
	}
	p.mu.Unlock()
	if e == nil {
		return nil
	}

	close(e.stopIdle)
	<-e.idleDone

	if e.dead || e.baseURL != baseURL || e.accountID != accountID {
		e.conn.Close()
		return nil
	}
	return e
}

// Put parks a healthy connection under key and starts its keepalive. The
// entry's coordination channels are (re)initialized here; callers only fill
// the connection, continuation, and identity fields.
func (p *WSPool) Put(key string, e *wsPoolEntry) {
	if p == nil || key == "" || e == nil || e.conn == nil {
		return
	}
	e.stopIdle = make(chan struct{})
	e.idleDone = make(chan struct{})
	e.lastUsed = time.Now()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		e.conn.Close()
		return
	}
	if old := p.entries[key]; old != nil {
		// Turns are serial per session so this shouldn't happen; never leak a
		// connection if it somehow does.
		delete(p.entries, key)
		close(old.stopIdle)
		old.conn.Close()
	}
	if len(p.entries) >= wsPoolMaxEntries {
		var lruKey string
		var lru *wsPoolEntry
		for k, v := range p.entries {
			if lru == nil || v.lastUsed.Before(lru.lastUsed) {
				lruKey, lru = k, v
			}
		}
		delete(p.entries, lruKey)
		close(lru.stopIdle)
		lru.conn.Close()
		logger.Info("openai ws pool evicted LRU entry", "key", lruKey)
	}
	p.entries[key] = e
	p.mu.Unlock()

	go e.keepalive(p, key)
}

// CloseAll tears down every parked connection and waits for the keepalive
// goroutines to exit. The pool accepts no further Puts afterwards.
func (p *WSPool) CloseAll() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	entries := p.entries
	p.entries = make(map[string]*wsPoolEntry)
	p.mu.Unlock()
	for _, e := range entries {
		close(e.stopIdle)
		e.conn.Close()
	}
	for _, e := range entries {
		<-e.idleDone
	}
}

// keepalive pings the peer at wsPingInterval so intermediaries keep the path
// open, expires the entry after wsPoolIdleTTL, and removes the entry on a
// ping-write failure (an RST'd socket fails its next write). It never reads —
// see the WSPool doc comment for why that is a hard gorilla constraint, not a
// choice.
func (e *wsPoolEntry) keepalive(p *WSPool, key string) {
	defer close(e.idleDone)

	expiry := time.NewTimer(wsPoolIdleTTL)
	defer expiry.Stop()
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()

	die := func(reason string, err error) {
		e.dead = true
		p.mu.Lock()
		if p.entries[key] == e {
			delete(p.entries, key)
		}
		p.mu.Unlock()
		e.conn.Close()
		logger.Info("openai ws pool entry closed", "key", key, "reason", reason, "err", err)
	}

	for {
		select {
		case <-e.stopIdle:
			return
		case <-expiry.C:
			die("idle ttl expired", nil)
			return
		case <-ping.C:
			if err := e.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				die("keepalive ping failed", err)
				return
			}
		}
	}
}
