package channel

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/klauspost/compress/gzhttp"
	"github.com/linanwx/nagobot/auth"
	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/push"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

const (
	webMainSessionID     = "cli"
	webMessageBufferSize = 100
	webDefaultAddr       = "127.0.0.1:18080"
	webShutdownTimeout   = 5 * time.Second
	sessionsDirName      = "sessions"

	// wsCloseReplaced tells a client its session binding was taken over by a
	// newer page. The client must NOT auto-reconnect (that would kick the new
	// page right back — an endless tug-of-war); it shows a takeover notice
	// with a manual "continue here" action instead.
	wsCloseReplaced = websocket.StatusCode(4001)
)

//go:embed web/dist/*
var rawFrontendFS embed.FS

// WebChannel implements the Channel interface for browser chat.
type WebChannel struct {
	addr      string
	workspace string
	authMgr   *auth.Manager
	pushMgr   *push.Manager
	messages  chan *Message
	done      chan struct{}
	wg        sync.WaitGroup
	server    *http.Server

	mu sync.RWMutex
	// clients: session key → viewer key → bound page. One active page per
	// VIEWER per session (a person's newer page displaces their older one via
	// wsCloseReplaced); different viewers coexist — that is what makes shared
	// sessions group-viewable. Unauthenticated connections share one "anon"
	// viewer key, preserving the old single-page semantics for auth-off
	// deployments.
	clients  map[string]map[string]*wsClient
	peers    map[*wsClient]struct{}
	msgID    int64
	stopOnce sync.Once

	// members: session key → person IDs that ever SENT a message there via
	// web. Drives person-filtered push: participants get pinged when they are
	// not watching; non-participants are never notified about the session.
	// Persisted to system/web_session_members.json.
	membersMu sync.Mutex
	members   map[string]map[string]bool

	systemPromptFn  func(string) (string, bool)
	toolDefsFn      func(string) ([]provider.ToolDef, bool)
	contextBudgetFn func(string) (int, int, bool)
	quoteFn         func(context.Context, string, string) (string, error)
}

type wsClient struct {
	conn         *websocket.Conn
	mu           sync.Mutex
	boundSession string        // session key this client is bound to
	person       *auth.Person  // authenticated person, nil on exempt/disabled-auth connections
}

type webInboundMessage struct {
	Type      string            `json:"type"`
	ID        string            `json:"id,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	Text      string            `json:"text"`
	Media     []webInboundMedia `json:"media,omitempty"`
	// TZ is the browser's IANA timezone (Intl.DateTimeFormat().resolvedOptions()
	// .timeZone). Carried into the message metadata so wake frontmatter times
	// render in the user's device zone rather than the server's. Untrusted —
	// validated with time.LoadLocation before it is persisted or used.
	TZ string `json:"tz,omitempty"`
}

// webInboundMedia references a file the client already uploaded via
// POST /api/media. Name is the basename returned by that endpoint; the message
// handler resolves it under {workspace}/media and turns it into a media_summary.
type webInboundMedia struct {
	Name string `json:"name"`
	Mime string `json:"mime,omitempty"`
}

type webOutboundMessage struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`

	// type:"peer_message" — another viewer of the same session spoke; their
	// display name rides along so the bubble can be attributed.
	Sender string `json:"sender,omitempty"`

	// type:"stream" — live turn activity (see thread/msg.StreamEvent).
	Kind       string `json:"kind,omitempty"`         // thinking | text | tool_call | tool_result | round_end | turn_end
	Delta      string `json:"delta,omitempty"`        // thinking/text increment
	Snapshot   string `json:"snapshot,omitempty"`     // round-accumulated text (self-heal)
	Tool       string `json:"tool,omitempty"`         // tool name
	ToolCallID string `json:"tool_call_id,omitempty"` // tool pairing id
	Args       string `json:"args,omitempty"`         // tool args / result preview
	IsError    bool   `json:"is_error,omitempty"`     // tool_result error flag
	Seq        int    `json:"seq,omitempty"`          // per-turn monotonic sequence
}

// NewWebChannel creates a new web channel from config. authMgr guards the
// HTTP API and WS; nil means auth is off (tests).
func NewWebChannel(cfg *config.Config, authMgr *auth.Manager) Channel {
	addr := cfg.GetWebAddr()
	if addr == "" {
		addr = webDefaultAddr
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		logger.Warn("web channel: failed to get workspace path", "err", err)
	}

	var pushMgr *push.Manager
	if workspace != "" {
		pushMgr, err = push.NewManager(filepath.Join(workspace, "system"))
		if err != nil {
			// Push is an enhancement, not a prerequisite — the channel still
			// serves chat without it.
			logger.Warn("web channel: push disabled", "err", err)
		}
	}

	ch := &WebChannel{
		addr:      addr,
		workspace: workspace,
		authMgr:   authMgr,
		pushMgr:   pushMgr,
		messages:  make(chan *Message, webMessageBufferSize),
		done:      make(chan struct{}),
		clients:   make(map[string]map[string]*wsClient),
		peers:     make(map[*wsClient]struct{}),
		members:   make(map[string]map[string]bool),
	}
	ch.loadMembers()
	return ch
}

// SetSystemPromptFn sets a callback that builds the current system prompt
// for a given session key. Returns ("", false) if the thread is not in memory.
func (w *WebChannel) SetSystemPromptFn(fn func(string) (string, bool)) {
	w.systemPromptFn = fn
}

// SetToolDefsFn sets a callback that returns the current tool definitions
// for a given session key. Returns (nil, false) if the thread is not in memory.
func (w *WebChannel) SetToolDefsFn(fn func(string) ([]provider.ToolDef, bool)) {
	w.toolDefsFn = fn
}

// SetContextBudgetFn sets a callback that returns the effective context window
// and warn token for a given session key from the thread runtime.
func (w *WebChannel) SetContextBudgetFn(fn func(string) (int, int, bool)) {
	w.contextBudgetFn = fn
}

// SetQuoteFn sets the reply-quote generator behind POST /api/quote: given a
// session key and the text being replied to, it returns ONE line of markdown
// quote, leading "> " marker included. The channel treats it as an opaque
// text→text function — it neither builds nor parses quote syntax — so replacing
// the generator (today an LLM sibling turn) is a one-line change in serve.go.
func (w *WebChannel) SetQuoteFn(fn func(context.Context, string, string) (string, error)) {
	w.quoteFn = fn
}

// Name returns the channel name.
func (w *WebChannel) Name() string { return "web" }

// staticCacheHandler serves the embedded SPA with correct cache lifetimes.
// Files under /assets/ are content-hash-named and immutable, so they get a
// one-year immutable cache; everything else at the root (index.html, sw.js,
// manifest, icons) is served no-cache so a new deploy — with fresh asset
// hashes and an updated service worker — is picked up on the next load.
//
// Without this, the embed FS's zero ModTime means http.FileServer emits no
// Last-Modified and no ETag, so nothing is ever conditionally cached and the
// whole bundle is re-downloaded on every visit.
func staticCacheHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			rw.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(rw, r)
	})
}

// Start starts the web server.
func (w *WebChannel) Start(ctx context.Context) error {
	frontendFS, err := fs.Sub(rawFrontendFS, "web/dist")
	if err != nil {
		return fmt.Errorf("failed to load embedded web frontend: %w", err)
	}

	mux := http.NewServeMux()
	// Auth endpoints are public by design: they are the login door. Every
	// other API route and the WS require an exempt IP or a session cookie.
	mux.Handle("/api/auth/me", http.HandlerFunc(w.handleAuthMe))
	mux.Handle("/api/auth/redeem", http.HandlerFunc(w.handleAuthRedeem))
	mux.Handle("/api/auth/context", http.HandlerFunc(w.handleAuthContext))
	mux.Handle("/api/auth/setup", http.HandlerFunc(w.handleAuthSetup))
	mux.Handle("/api/auth/passkey/register/begin", http.HandlerFunc(w.handleRegisterBegin))
	mux.Handle("/api/auth/passkey/register/finish", http.HandlerFunc(w.handleRegisterFinish))
	mux.Handle("/api/auth/passkey/login/begin", http.HandlerFunc(w.handleLoginBegin))
	mux.Handle("/api/auth/passkey/login/finish", http.HandlerFunc(w.handleLoginFinish))
	mux.Handle("/api/auth/password/set", http.HandlerFunc(w.handlePasswordSet))
	mux.Handle("/api/auth/password/login", http.HandlerFunc(w.handlePasswordLogin))
	mux.Handle("/api/auth/logout", http.HandlerFunc(w.handleLogout))
	mux.Handle("/api/history", w.protected(w.handleHistory))
	mux.Handle("/api/sessions/", w.protected(w.handleSessionMessages))
	mux.Handle("/api/sessions", w.protected(w.handleSessions))
	mux.Handle("/api/config", w.protected(w.handleConfig))
	mux.Handle("/api/prompts/", w.protected(w.handlePromptFile))
	mux.Handle("/api/prompts", w.protected(w.handlePrompts))
	mux.Handle("/api/heartbeat/", w.protected(w.handleHeartbeat))
	mux.Handle("/api/media/", w.protected(w.handleMedia))
	mux.Handle("/api/media", w.protected(w.handleMediaUpload))
	mux.Handle("/api/push/vapid-key", w.protected(w.handlePushKey))
	mux.Handle("/api/push/subscribe", w.protected(w.handlePushSubscribe))
	mux.Handle("/api/push/unsubscribe", w.protected(w.handlePushUnsubscribe))
	mux.Handle("/api/quote", w.protected(w.handleQuote))
	mux.Handle("/", staticCacheHandler(frontendFS))

	// Compression + WS split. gzhttp wraps the whole API+static mux, but with an
	// explicit compressible-type ALLOW list: gzhttp's default compresses
	// application/octet-stream, and Go serves .woff2 fonts as octet-stream (they
	// aren't in its mime table), so the default would re-gzip already-compressed
	// fonts — wasted CPU, ~zero gain. The list below is exactly the text-shaped
	// types (HTML/JS/CSS/JSON/SVG); fonts, PNG icons and other binaries fall
	// through uncompressed.
	//
	// The WebSocket upgrade is registered on the OUTER router so it never passes
	// through the gzip ResponseWriter — coder/websocket's Accept hard-requires
	// http.Hijacker, which the gzip wrapper does not surface. "/ws" is a more
	// specific pattern than "/", so it wins the route; everything else falls to
	// the gzipped mux.
	gzip, err := gzhttp.NewWrapper(gzhttp.ContentTypes([]string{
		"text/html",
		"text/css",
		"text/javascript",
		"application/javascript",
		"application/json",
		"application/manifest+json",
		"image/svg+xml",
		"text/plain",
	}))
	if err != nil {
		return fmt.Errorf("failed to build gzip wrapper: %w", err)
	}
	root := http.NewServeMux()
	root.Handle("/ws", w.protected(w.handleWS))
	root.Handle("/", gzip(mux))

	w.server = &http.Server{
		Addr:    w.addr,
		Handler: root,
	}

	ln, err := net.Listen("tcp", w.addr)
	if err != nil {
		return fmt.Errorf("web channel listen failed on %s: %w", w.addr, err)
	}

	bindAddr := ln.Addr().String()
	logger.Info("web channel started", "addr", bindAddr, "url", webURLHintFromAddr(bindAddr))

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		if serveErr := w.server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("web channel server error", "err", serveErr)
		}
	}()

	return nil
}

// Stop gracefully stops the channel.
func (w *WebChannel) Stop() error {
	w.stopOnce.Do(func() {
		close(w.done)

		w.mu.Lock()
		clients := make([]*wsClient, 0, len(w.peers))
		for client := range w.peers {
			clients = append(clients, client)
		}
		w.clients = make(map[string]map[string]*wsClient)
		w.peers = make(map[*wsClient]struct{})
		w.mu.Unlock()

		for _, client := range clients {
			_ = client.conn.Close(websocket.StatusNormalClosure, "shutdown")
		}

		if w.server != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), webShutdownTimeout)
			defer cancel()
			if err := w.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Warn("web channel shutdown error", "err", err)
			}
		}

		w.wg.Wait()
		close(w.messages)
		logger.Info("web channel stopped")
	})
	return nil
}

// Send sends a response to the web client.
func (w *WebChannel) Send(ctx context.Context, resp *Response) error {
	if resp == nil {
		return fmt.Errorf("response is nil")
	}

	sessionID := sanitizeSessionKey(resp.ReplyTo)
	if sessionID == "" {
		sessionID = webMainSessionID
	}

	payload := webOutboundMessage{
		Type: "response",
		Text: resp.Text,
	}

	// Multicast to every page watching the session. A viewer whose write
	// fails counts as offline — their devices fall through to push below.
	delivered := 0
	online := make(map[string]bool)
	for _, client := range w.boundClients(sessionID) {
		client.mu.Lock()
		err := wsjson.Write(ctx, client.conn, payload)
		client.mu.Unlock()
		if err != nil {
			logger.Warn("web channel: ws send failed", "session", sessionID, "err", err)
			continue
		}
		delivered++
		online[viewerKey(client.person)] = true
	}

	notification := push.Notification{
		Title:   "nagobot · " + sessionID,
		Body:    truncateRunes(resp.Text, 140),
		Session: sessionID,
	}

	// Participants who are not watching right now get the ping on their
	// enrolled devices — that is what makes a shared session a group chat.
	if members := w.membersOf(sessionID); members != nil {
		for id := range members {
			if online[id] {
				delete(members, id)
			}
		}
		pushed := w.pushMgr.SendTo(notification, members)
		if delivered > 0 || pushed > 0 {
			return nil
		}
		return fmt.Errorf("web session not connected: %s", sessionID)
	}

	// No participant record (pre-existing session, or auth-off deployment):
	// keep the legacy behavior — broadcast push only when no page at all is
	// watching.
	if delivered > 0 {
		return nil
	}
	if w.pushMgr.HasSubscriptions() {
		w.pushMgr.Send(notification)
		return nil
	}
	return fmt.Errorf("web session not connected: %s", sessionID)
}

// Messages returns the incoming message channel.
func (w *WebChannel) Messages() <-chan *Message { return w.messages }

// StreamTo implements Streamer: forward one live stream event to every page
// bound to the session. No page bound = silently dropped (nobody is watching;
// the authoritative content still arrives via Send and the session history).
// Rebinding mid-turn picks up the stream from the next event — snapshots make
// that seamless. Per-page write failures are dropped for the same reason.
func (w *WebChannel) StreamTo(ctx context.Context, replyTo string, ev thread.StreamEvent) error {
	sessionID := sanitizeSessionKey(replyTo)
	if sessionID == "" {
		sessionID = webMainSessionID
	}

	payload := webOutboundMessage{
		Type:       "stream",
		Kind:       string(ev.Type),
		Delta:      ev.Delta,
		Snapshot:   ev.Snapshot,
		Tool:       ev.Tool,
		ToolCallID: ev.ToolCallID,
		Args:       ev.Args,
		IsError:    ev.IsError,
		Seq:        ev.Seq,
	}

	for _, client := range w.boundClients(sessionID) {
		client.mu.Lock()
		_ = wsjson.Write(ctx, client.conn, payload)
		client.mu.Unlock()
	}
	return nil
}

func (w *WebChannel) handleWS(rw http.ResponseWriter, r *http.Request) {
	// The protected() wrapper already gated access; re-resolve to learn WHO
	// this is (nil on exempt-IP connections) for message attribution.
	person, _ := w.authorize(r)

	conn, err := websocket.Accept(rw, r, nil)
	if err != nil {
		return
	}

	// Deliberately no bindClient here: binding happens only on the client's
	// explicit "bind" frame. Binding every fresh connection to the main
	// session would kick whichever page is legitimately watching it, even
	// when this connection is about to bind a different session.
	// No default bound session: a connection that never sent a bind frame has no
	// session, and a message on it is refused rather than silently routed into
	// a shared default. (unbindClient on an empty key is a no-op.)
	client := &wsClient{conn: conn, boundSession: "", person: person}
	w.registerPeer(client)

	w.wg.Add(1)
	defer w.wg.Done()
	defer func() {
		w.unregisterPeer(client)
		client.mu.Lock()
		bound := client.boundSession
		client.mu.Unlock()
		w.unbindClient(bound, client)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		var req webInboundMessage
		if err := wsjson.Read(r.Context(), conn, &req); err != nil {
			return
		}

		reqType := strings.TrimSpace(req.Type)
		if reqType == "" {
			reqType = "message"
		}

		switch reqType {
		case "bind":
			sid := sanitizeSessionKey(strings.TrimSpace(req.SessionID))
			if sid == "" {
				_ = wsjson.Write(r.Context(), conn, webOutboundMessage{Type: "error", Error: "invalid session_id"})
				continue
			}
			client.mu.Lock()
			oldSession := client.boundSession
			client.boundSession = sid
			client.mu.Unlock()
			w.unbindClient(oldSession, client)
			w.bindClient(sid, client)
			_ = wsjson.Write(r.Context(), conn, webOutboundMessage{Type: "bound", Text: sid})

		case "message":
			text := strings.TrimSpace(req.Text)

			// Resolve any client-uploaded media (POST /api/media returned these
			// basenames) into a media_summary — the same channel-agnostic
			// contract Telegram/Discord use. Each name is basename-cleaned and
			// must exist under {workspace}/media, so a client cannot smuggle an
			// arbitrary path into image_path (which the model may later read).
			var mediaSummaries []string
			if w.workspace != "" {
				for _, m := range req.Media {
					name := filepath.Base(filepath.Clean(strings.TrimSpace(m.Name)))
					if name == "" || name == "." || name == string(filepath.Separator) {
						continue
					}
					path := filepath.Join(w.workspace, "media", name)
					if _, err := os.Stat(path); err != nil {
						continue
					}
					mediaSummaries = append(mediaSummaries, MediaSummary("photo", "image_path", path))
				}
			}

			if text == "" && len(mediaSummaries) == 0 {
				continue
			}
			if text == "" {
				// Image-only turn: give the wake a body, mirroring Telegram's
				// "[Photo received]" placeholder for a caption-less photo.
				text = "[Image received]"
			}

			client.mu.Lock()
			boundSess := client.boundSession
			client.mu.Unlock()

			sessionID := boundSess
			if sid := strings.TrimSpace(req.SessionID); sid != "" {
				if valid := sanitizeSessionKey(sid); valid != "" {
					sessionID = valid
				}
			}
			if sessionID == "" {
				_ = wsjson.Write(r.Context(), conn, webOutboundMessage{
					Type:  "error",
					Error: "no session bound: send a bind frame or include session_id",
				})
				continue
			}
			channelID := "web:" + sessionID

			username := "web-user"
			metadata := map[string]string{
				"chat_id": sessionID,
			}
			if len(mediaSummaries) > 0 {
				metadata["media_summary"] = strings.Join(mediaSummaries, "\n")
			}
			// Browser timezone: validate here (the trust boundary) so a garbage
			// or unknown zone never reaches the session store. LoadLocation
			// rejects anything not in the tz database.
			if tz := strings.TrimSpace(req.TZ); tz != "" {
				if _, err := time.LoadLocation(tz); err == nil {
					metadata["client_tz"] = tz
				}
			}
			if client.person != nil {
				// Attribute the message to the logged-in person so rendering
				// can align it with their channel identities.
				username = client.person.Username
				metadata["person_id"] = client.person.ID
			}
			msg := &Message{
				ID:        fmt.Sprintf("web-%d", atomic.AddInt64(&w.msgID, 1)),
				ChannelID: channelID,
				UserID:    sessionID,
				Username:  username,
				Text:      text,
				Metadata:  metadata,
			}

			select {
			case w.messages <- msg:
			case <-w.done:
				return
			case <-r.Context().Done():
				return
			}

			// Group bookkeeping: the sender becomes a participant (push
			// target when away), and every OTHER page watching the session
			// sees the message immediately — without this they would only
			// see the reply stream, not what prompted it.
			if client.person != nil {
				w.recordMember(sessionID, client.person.ID)
			}
			peerPayload := webOutboundMessage{
				Type:   "peer_message",
				Text:   text,
				Sender: username,
			}
			for _, peer := range w.boundClients(sessionID) {
				if peer == client {
					continue
				}
				peer.mu.Lock()
				_ = wsjson.Write(r.Context(), peer.conn, peerPayload)
				peer.mu.Unlock()
			}

		default:
			_ = wsjson.Write(r.Context(), conn, webOutboundMessage{Type: "error", Error: "unsupported message type"})
		}
	}
}

// viewerKey identifies who is looking through a ws client for binding
// purposes: the person ID, or a shared "anon" bucket when auth is off or the
// connection came from an exempt IP.
func viewerKey(person *auth.Person) string {
	if person == nil {
		return "anon"
	}
	return person.ID
}

func (w *WebChannel) membersFile() string {
	if w.workspace == "" {
		return ""
	}
	return filepath.Join(w.workspace, "system", "web_session_members.json")
}

func (w *WebChannel) loadMembers() {
	path := w.membersFile()
	if path == "" {
		return
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return // first run
	}
	var raw map[string][]string
	if err := json.Unmarshal(buf, &raw); err != nil {
		logger.Warn("web channel: bad session members file", "path", path, "err", err)
		return
	}
	w.membersMu.Lock()
	defer w.membersMu.Unlock()
	for session, ids := range raw {
		set := make(map[string]bool, len(ids))
		for _, id := range ids {
			set[id] = true
		}
		w.members[session] = set
	}
}

func (w *WebChannel) recordMember(sessionID, personID string) {
	if sessionID == "" || personID == "" {
		return
	}
	w.membersMu.Lock()
	defer w.membersMu.Unlock()
	set := w.members[sessionID]
	if set[personID] {
		return
	}
	if set == nil {
		set = make(map[string]bool)
		w.members[sessionID] = set
	}
	set[personID] = true

	path := w.membersFile()
	if path == "" {
		return
	}
	raw := make(map[string][]string, len(w.members))
	for session, ids := range w.members {
		list := make([]string, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		sort.Strings(list)
		raw[session] = list
	}
	buf, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		logger.Warn("web channel: marshal session members failed", "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		if err := os.WriteFile(path, buf, 0o600); err != nil {
			logger.Warn("web channel: save session members failed", "err", err)
		}
	}
}

// membersOf returns the participant person IDs of a session, or nil when the
// session has no recorded participants.
func (w *WebChannel) membersOf(sessionID string) map[string]bool {
	w.membersMu.Lock()
	defer w.membersMu.Unlock()
	set := w.members[sessionID]
	if len(set) == 0 {
		return nil
	}
	out := make(map[string]bool, len(set))
	for id := range set {
		out[id] = true
	}
	return out
}

func (w *WebChannel) registerPeer(client *wsClient) {
	w.mu.Lock()
	w.peers[client] = struct{}{}
	w.mu.Unlock()
}

func (w *WebChannel) unregisterPeer(client *wsClient) {
	w.mu.Lock()
	delete(w.peers, client)
	w.mu.Unlock()
}

func (w *WebChannel) bindClient(sessionID string, client *wsClient) {
	key := viewerKey(client.person)
	w.mu.Lock()
	viewers := w.clients[sessionID]
	if viewers == nil {
		viewers = make(map[string]*wsClient)
		w.clients[sessionID] = viewers
	}
	old := viewers[key]
	viewers[key] = client
	w.mu.Unlock()

	// Only the SAME viewer's older page is displaced — other people watching
	// the session keep their connections (group viewing).
	if old != nil && old != client {
		_ = old.conn.Close(wsCloseReplaced, "replaced by another page")
	}
}

func (w *WebChannel) unbindClient(sessionID string, client *wsClient) {
	key := viewerKey(client.person)
	w.mu.Lock()
	defer w.mu.Unlock()
	viewers := w.clients[sessionID]
	if viewers[key] == client {
		delete(viewers, key)
		if len(viewers) == 0 {
			delete(w.clients, sessionID)
		}
	}
}

// boundClients snapshots every page currently watching a session.
func (w *WebChannel) boundClients(sessionID string) []*wsClient {
	w.mu.RLock()
	defer w.mu.RUnlock()
	viewers := w.clients[sessionID]
	if len(viewers) == 0 {
		return nil
	}
	out := make([]*wsClient, 0, len(viewers))
	for _, c := range viewers {
		out = append(out, c)
	}
	return out
}

func webURLHintFromAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}

	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}

	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

type webHistoryEnvelope struct {
	SessionID  string              `json:"session_id"`
	SessionKey string              `json:"session_key"`
	Messages   []webHistoryMessage `json:"messages"`
}

type webHistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}


func (w *WebChannel) handleHistory(rw http.ResponseWriter, r *http.Request) {
	history, err := w.loadHistory()
	if err != nil {
		http.Error(rw, fmt.Sprintf("failed to load history: %v", err), http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(webHistoryEnvelope{
		SessionID:  webMainSessionID,
		SessionKey: webMainSessionID,
		Messages:   history,
	})
}

func (w *WebChannel) loadHistory() ([]webHistoryMessage, error) {
	if w.workspace == "" {
		return nil, fmt.Errorf("workspace is not configured")
	}

	path := filepath.Join(w.workspace, sessionsDirName, "cli", session.SessionFileName)
	s, err := session.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []webHistoryMessage{}, nil
		}
		return nil, err
	}

	out := make([]webHistoryMessage, 0, len(s.Messages))
	for _, m := range s.Messages {
		role := strings.TrimSpace(m.Role)
		content := strings.TrimSpace(m.Content)
		if role == "" || content == "" {
			continue
		}
		out = append(out, webHistoryMessage{Role: role, Content: content})
	}
	return out, nil
}

// sanitizeSessionKey validates a session key (allows colons for keys like "telegram:12345").
func sanitizeSessionKey(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 128 {
		return ""
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ':' || r == '.' {
			continue
		}
		return ""
	}
	return s
}

// parseKeyFromPath extracts a session key from a URL path by stripping the given
// prefix and converting "/" separators to ":".
func parseKeyFromPath(urlPath, prefix string) string {
	raw := strings.TrimPrefix(urlPath, prefix)
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		return ""
	}
	return strings.ReplaceAll(raw, "/", ":")
}

// resolveSessionFile resolves a session key to a safe filesystem path within
// the sessions directory. Returns empty string if the path would escape
// the sessions directory (path traversal protection).
func (w *WebChannel) resolveSessionFile(key, filename string) string {
	sessionsDir := filepath.Join(w.workspace, sessionsDirName)
	keyPath := strings.ReplaceAll(key, ":", string(filepath.Separator))
	resolved := filepath.Clean(filepath.Join(sessionsDir, keyPath, filename))
	// Ensure the resolved path stays within the sessions directory.
	if !strings.HasPrefix(resolved, filepath.Clean(sessionsDir)+string(filepath.Separator)) {
		return ""
	}
	return resolved
}

// --- GET /api/sessions ---

type sessionListEntry struct {
	Key          string    `json:"key"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	HasHeartbeat bool      `json:"has_heartbeat,omitempty"`
	Summary      string    `json:"summary,omitempty"`
}

func (w *WebChannel) handleSessions(rw http.ResponseWriter, r *http.Request) {
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}

	sessionsDir := filepath.Join(w.workspace, sessionsDirName)
	summaries := loadWebSummaries(filepath.Join(w.workspace, "system", "sessions_summary.json"))
	var entries []sessionListEntry

	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if d.IsDir() || d.Name() != session.SessionFileName {
			return nil
		}

		key := session.DeriveKeyFromPath(path)

		lineCount := countLines(path)
		updatedAt, _ := session.ReadUpdatedAt(path)

		// Check for heartbeat.md — only for non-cron/thread sessions active within 2 days.
		hasHB := false
		isCronOrThread := strings.HasPrefix(key, "cron:") || strings.Contains(key, ":threads:")
		if !isCronOrThread {
			hbPath := filepath.Join(filepath.Dir(path), "heartbeat.md")
			hbCutoff := time.Now().AddDate(0, 0, -2)
			if updatedAt.After(hbCutoff) {
				if fi, err := os.Stat(hbPath); err == nil && fi.Size() > 0 {
					hasHB = true
				}
			}
		}

		entry := sessionListEntry{
			Key:          key,
			UpdatedAt:    updatedAt,
			MessageCount: lineCount,
			HasHeartbeat: hasHB,
		}
		if s, ok := summaries[key]; ok {
			entry.Summary = s
		}
		entries = append(entries, entry)
		return nil
	})

	// Sort by updated_at descending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})

	if entries == nil {
		entries = []sessionListEntry{}
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(entries)
}

// loadWebSummaries reads system/sessions_summary.json and returns key→summary text.
func loadWebSummaries(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	m := make(map[string]string, len(raw))
	for k, v := range raw {
		if v.Summary != "" {
			m[k] = v.Summary
		}
	}
	return m
}

// countLines counts newlines in a file without parsing JSON. Returns 0 on error.
func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			count++
		}
	}
	return count
}

// --- GET /api/sessions/{key...} ---

type sessionDetail struct {
	Key       string           `json:"key"`
	Messages  []messageWithTok `json:"messages"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type messageWithTok struct {
	provider.Message
	Tokens           int `json:"tokens"`
	CompressedTokens int `json:"compressed_tokens,omitempty"`
}

func (w *WebChannel) handleSessionMessages(rw http.ResponseWriter, r *http.Request) {
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}

	raw := parseKeyFromPath(r.URL.Path, "/api/sessions/")
	if raw == "" {
		http.Error(rw, "missing session key", http.StatusBadRequest)
		return
	}

	// Route: /api/sessions/{key...}/system-prompt
	// parseKeyFromPath converts "/" to ":", so the suffix becomes ":system-prompt".
	if key, ok := strings.CutSuffix(raw, ":system-prompt"); ok {
		w.handleSystemPrompt(rw, key)
		return
	}

	// Route: /api/sessions/{key...}/tools
	if key, ok := strings.CutSuffix(raw, ":tools"); ok {
		w.handleToolDefs(rw, key)
		return
	}

	// Route: /api/sessions/{key...}/stats
	if key, ok := strings.CutSuffix(raw, ":stats"); ok {
		w.handleSessionStats(rw, key)
		return
	}

	// Route: /api/sessions/{key...}/chat
	if key, ok := strings.CutSuffix(raw, ":chat"); ok {
		w.handleSessionChat(rw, key)
		return
	}

	// Route: /api/sessions/{key...}/files
	if key, ok := strings.CutSuffix(raw, ":files"); ok {
		w.handleSessionFiles(rw, key)
		return
	}
	key := raw

	path := w.resolveSessionFile(key, session.SessionFileName)
	if path == "" {
		http.Error(rw, "invalid session key", http.StatusBadRequest)
		return
	}

	s, err := session.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(rw, "session not found", http.StatusNotFound)
			return
		}
		http.Error(rw, fmt.Sprintf("failed to read session: %v", err), http.StatusInternalServerError)
		return
	}

	msgs := make([]messageWithTok, len(s.Messages))
	for i, m := range s.Messages {
		mt := messageWithTok{
			Message: m,
			Tokens:  thread.EstimateMessageTokens(m),
		}
		if m.Compressed != "" || m.ReasoningTrimmed || m.HeartbeatTrim {
			applied := thread.ApplyCompressedMessage(m)
			mt.CompressedTokens = thread.EstimateMessageTokens(applied)
		}
		msgs[i] = mt
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(sessionDetail{
		Key:       key,
		Messages:  msgs,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	})
}

// --- GET /api/sessions/{key...}/chat ---

// chatLogLimit caps how many chat.jsonl entries a single request returns; old
// sessions accumulate thousands and the chat view only renders the tail.
const chatLogLimit = 500

type sessionChatResponse struct {
	Key      string              `json:"key"`
	Messages []session.ChatEntry `json:"messages"`
}

// handleSessionChat serves the session's chat.jsonl — the clean user-facing
// conversation log (delivered assistant replies included, wake frontmatter and
// tool traffic excluded). Sessions without a chat log (e.g. cron runners)
// return 404 so clients can fall back to rendering session.jsonl.
func (w *WebChannel) handleSessionChat(rw http.ResponseWriter, key string) {
	marker := w.resolveSessionFile(key, session.SessionFileName)
	if marker == "" {
		http.Error(rw, "invalid session key", http.StatusBadRequest)
		return
	}

	entries, err := session.ReadChatEntries(filepath.Dir(marker), chatLogLimit)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(rw, "no chat log for session", http.StatusNotFound)
			return
		}
		http.Error(rw, fmt.Sprintf("failed to read chat log: %v", err), http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []session.ChatEntry{}
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(sessionChatResponse{Key: key, Messages: entries})
}

// --- GET /api/sessions/{key...}/system-prompt ---

type systemPromptResponse struct {
	Key       string `json:"key"`
	Prompt    string `json:"prompt,omitempty"`
	Available bool   `json:"available"`
	Tokens    int    `json:"tokens,omitempty"`
}

func (w *WebChannel) handleSystemPrompt(rw http.ResponseWriter, key string) {
	resp := systemPromptResponse{Key: key}
	if w.systemPromptFn != nil {
		resp.Prompt, resp.Available = w.systemPromptFn(key)
		if resp.Available && resp.Prompt != "" {
			resp.Tokens = thread.EstimateTextTokens(resp.Prompt)
		}
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(resp)
}

// --- GET /api/sessions/{key...}/tools ---

type toolDefsResponse struct {
	Key       string             `json:"key"`
	Tools     []provider.ToolDef `json:"tools,omitempty"`
	Available bool               `json:"available"`
	Count     int                `json:"count,omitempty"`
	Tokens    int                `json:"tokens,omitempty"`
}

func (w *WebChannel) handleToolDefs(rw http.ResponseWriter, key string) {
	resp := toolDefsResponse{Key: key}
	if w.toolDefsFn != nil {
		resp.Tools, resp.Available = w.toolDefsFn(key)
		if resp.Available {
			resp.Count = len(resp.Tools)
			resp.Tokens = thread.EstimateToolDefsTokens(resp.Tools)
		}
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(resp)
}

// --- GET /api/sessions/{key...}/stats ---

type sessionStatsResponse struct {
	Key                 string          `json:"key"`
	MessageCount        int             `json:"message_count"`
	RoleCounts          map[string]int  `json:"role_counts"`
	CompressedMessages  int             `json:"compressed_messages"`
	RoleTokens          map[string]int  `json:"role_tokens"`
	RawTokens           int             `json:"raw_tokens"`
	CompressedTokens    int             `json:"compressed_tokens"`
	TokensSaved         int             `json:"tokens_saved"`
	ContextWindowTokens int             `json:"context_window_tokens"`
	UsagePercent        float64         `json:"usage_percent"`
	Tier2TriggerPercent float64         `json:"tier2_trigger_percent"`
	Tier3TriggerPercent float64         `json:"tier3_trigger_percent"`
	PressureStatus      string          `json:"pressure_status"`
	IsRuntime           bool            `json:"is_runtime"`
	TokenBreakdown      *tokenBreakdown `json:"token_breakdown,omitempty"`
}

type tokenBreakdown struct {
	BySource    map[string]int   `json:"by_source"`
	ByRole      map[string]int   `json:"by_role"`
	Compression compressionStats `json:"compression"`
}

type compressionStats struct {
	RawTokens        int `json:"raw_tokens"`
	CompressedTokens int `json:"compressed_tokens"`
	SavedTokens      int `json:"saved_tokens"`
}

func (w *WebChannel) handleSessionStats(rw http.ResponseWriter, key string) {
	path := w.resolveSessionFile(key, session.SessionFileName)
	if path == "" {
		http.Error(rw, "invalid session key", http.StatusBadRequest)
		return
	}
	s, err := session.ReadFile(path)
	if err != nil {
		http.Error(rw, "session not found", http.StatusNotFound)
		return
	}

	messages := s.Messages
	roleCounts := map[string]int{}
	compressedCount := 0
	for _, m := range messages {
		roleCounts[m.Role]++
		if m.Compressed != "" {
			compressedCount++
		}
	}

	rawTokens := thread.EstimateMessagesTokens(messages)
	compressed := thread.ApplyCompressed(provider.SanitizeMessages(messages))
	compressedTokens := thread.EstimateMessagesTokens(compressed)

	roleTokens := map[string]int{}
	sourceTokens := map[string]int{}
	for _, m := range compressed {
		tok := thread.EstimateMessageTokens(m)
		roleTokens[m.Role] += tok
		src := m.Source
		if src == "" {
			src = "(no source)"
		}
		sourceTokens[src] += tok
	}

	// Try to get context window from thread runtime; fall back to global config.
	var contextWindow int
	var isRuntime bool
	if w.contextBudgetFn != nil {
		if tw, _, ok := w.contextBudgetFn(key); ok {
			contextWindow = tw
			isRuntime = true
		}
	}
	if contextWindow == 0 {
		cfg, _ := config.Load()
		if cfg == nil {
			cfg = config.DefaultConfig()
		}
		contextWindow = provider.EffectiveContextWindow(cfg.GetProvider(), cfg.GetModelName(), cfg.GetContextWindowTokens())
	}
	ct := thread.ComputeContextThresholds(contextWindow)

	var usagePercent float64
	if ct.ContextWindow > 0 {
		usagePercent = float64(compressedTokens) / float64(ct.ContextWindow) * 100
	}
	status := thread.PressureStatus(compressedTokens, ct)

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(sessionStatsResponse{
		Key:                 key,
		MessageCount:        len(messages),
		RoleCounts:          roleCounts,
		CompressedMessages:  compressedCount,
		RoleTokens:          roleTokens,
		RawTokens:           rawTokens,
		CompressedTokens:    compressedTokens,
		TokensSaved:         rawTokens - compressedTokens,
		ContextWindowTokens: contextWindow,
		UsagePercent:        usagePercent,
		Tier2TriggerPercent: ct.Tier2TriggerPercent(),
		Tier3TriggerPercent: ct.Tier3TriggerPercent(),
		PressureStatus:      status,
		IsRuntime:           isRuntime,
		TokenBreakdown: &tokenBreakdown{
			BySource: sourceTokens,
			ByRole:   roleTokens,
			Compression: compressionStats{
				RawTokens:        rawTokens,
				CompressedTokens: compressedTokens,
				SavedTokens:      rawTokens - compressedTokens,
			},
		},
	})
}

// --- GET /api/config ---

func (w *WebChannel) handleConfig(rw http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		http.Error(rw, fmt.Sprintf("failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	// Merge user-created cron jobs from cron.jsonl into cfg.Cron.
	if w.workspace != "" {
		storePath := filepath.Join(w.workspace, "system", "cron.jsonl")
		if storeJobs, err := cronpkg.ReadJobs(storePath); err == nil {
			seedIDs := make(map[string]struct{}, len(cfg.Cron))
			for _, j := range cfg.Cron {
				seedIDs[j.ID] = struct{}{}
			}
			for _, j := range storeJobs {
				if _, dup := seedIDs[j.ID]; !dup {
					cfg.Cron = append(cfg.Cron, j)
				}
			}
		}
	}

	// Round-trip through a generic map so redaction can walk every field by
	// name instead of relying on a hand-maintained per-provider list (which
	// silently leaked each newly added provider's key).
	raw, err := json.Marshal(cfg)
	if err != nil {
		http.Error(rw, fmt.Sprintf("failed to encode config: %v", err), http.StatusInternalServerError)
		return
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		http.Error(rw, fmt.Sprintf("failed to decode config: %v", err), http.StatusInternalServerError)
		return
	}
	redactTree(tree, false)

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(tree)
}

const redactedValue = "***configured***"

// secretKeyRe matches field names that carry credentials. Substring match on
// the lowercased name: apiKey, jinaKey, accessKeyId, secretAccessKey,
// accessToken, refreshToken, appSecret, password, … "env" is included because
// env vars routinely hold tokens (HASS_TOKEN et al).
var secretKeyRe = regexp.MustCompile(`key|token|secret|password|credential|env`)

// redactTree walks a decoded JSON tree and blanks every non-empty string
// whose own field name — or any ancestor's — looks secret-bearing. Ancestor
// propagation is what catches map values under a secret-named parent
// ("search.keys.google", every "env" entry) whose leaf names are innocent.
// False positives (e.g. "tokenType": "Bearer") are accepted: over-redacting
// display data is safe, leaking one real key is not.
func redactTree(node any, secretScope bool) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			inScope := secretScope || secretKeyRe.MatchString(strings.ToLower(k))
			if s, ok := child.(string); ok {
				if inScope && s != "" {
					v[k] = redactedValue
				}
				continue
			}
			redactTree(child, inScope)
		}
	case []any:
		for i, child := range v {
			if s, ok := child.(string); ok {
				if secretScope && s != "" {
					v[i] = redactedValue
				}
				continue
			}
			redactTree(child, secretScope)
		}
	}
}

// --- GET /api/prompts ---
//
// Lists the global prompt files under {workspace}/system that the runtime
// actually injects into agent prompts: GLOBAL.md / world_knowledge.md /
// people_knowledge.md (agent.Build injections) and the system-prompt sections
// in system/sections (agent.SectionRegistry). Curated whitelist, not a
// directory scan — legacy leftovers (CORE_MECHANISM.md, WEB_*_GUIDE.md) have
// no runtime consumer and are deliberately absent. Read-only.

type promptFileEntry struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Size        int64  `json:"size"`
	Modified    string `json:"modified"`
}

// globalPromptFiles is the whitelist, in display order. Name is the path
// relative to {workspace}/system and doubles as the /api/prompts/{name} key.
var globalPromptFiles = []promptFileEntry{
	{Name: "GLOBAL.md", Label: "Global persona", Description: "Role and persona instructions injected into every user-facing agent."},
	{Name: "world_knowledge.md", Label: "World knowledge", Description: "Recent world events beyond the model training cutoff; rewritten nightly by the world-knowledge cron."},
	{Name: "people_knowledge.md", Label: "People knowledge", Description: "Cross-session knowledge about people, with dated facts and confidence."},
	{Name: "sections/how-nagobot-works.md", Label: "How nagobot works", Description: "System-prompt section: the runtime model every agent is briefed with."},
	{Name: "sections/context.md", Label: "Context", Description: "System-prompt section: date, session, and environment placeholders."},
	{Name: "sections/tools.md", Label: "Tools", Description: "System-prompt section: tool list injection."},
	{Name: "sections/skills.md", Label: "Skills", Description: "System-prompt section: skill list injection."},
	{Name: "sections/agent-definitions.md", Label: "Agent definitions", Description: "System-prompt section: available agent names."},
	{Name: "sections/active-sessions.md", Label: "Active sessions", Description: "System-prompt section: cross-session awareness summary."},
}

func (w *WebChannel) handlePrompts(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}
	files := []promptFileEntry{}
	for _, spec := range globalPromptFiles {
		info, err := os.Stat(filepath.Join(w.workspace, "system", filepath.FromSlash(spec.Name)))
		if err != nil {
			continue // not present in this workspace — skip, keep order
		}
		entry := spec
		entry.Size = info.Size()
		entry.Modified = info.ModTime().Format(time.RFC3339)
		files = append(files, entry)
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]any{"files": files})
}

// --- GET /api/prompts/{name} ---

func (w *WebChannel) handlePromptFile(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/prompts/")
	// Exact whitelist match — the only paths this handler can ever read.
	allowed := false
	for _, spec := range globalPromptFiles {
		if spec.Name == name {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(rw, "unknown prompt file", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(filepath.Join(w.workspace, "system", filepath.FromSlash(name)))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(rw, "prompt file not found", http.StatusNotFound)
			return
		}
		http.Error(rw, fmt.Sprintf("failed to read prompt file: %v", err), http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]string{"name": name, "content": string(data)})
}

// --- GET /api/heartbeat/{key...} ---

type heartbeatResponse struct {
	Key     string `json:"key"`
	Content string `json:"content"`
}

func (w *WebChannel) handleHeartbeat(rw http.ResponseWriter, r *http.Request) {
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}

	key := parseKeyFromPath(r.URL.Path, "/api/heartbeat/")
	if key == "" {
		http.Error(rw, "missing session key", http.StatusBadRequest)
		return
	}

	path := w.resolveSessionFile(key, "heartbeat.md")
	if path == "" {
		http.Error(rw, "invalid session key", http.StatusBadRequest)
		return
	}

	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(heartbeatResponse{Key: key, Content: ""})
			return
		}
		http.Error(rw, fmt.Sprintf("failed to read heartbeat: %v", err), http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(heartbeatResponse{Key: key, Content: string(content)})
}

// --- GET /api/sessions/{key...}/files ---

type sessionMarkdownFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type sessionFilesResponse struct {
	Key   string                `json:"key"`
	Files []sessionMarkdownFile `json:"files"`
}

// handleSessionFiles lists every top-level .md file in the session's directory
// (USER.md, dream.md, heartbeat.md, ...) along with its content, so the UI can
// browse the session's markdown memory files. Subdirectories are not descended.
func (w *WebChannel) handleSessionFiles(rw http.ResponseWriter, key string) {
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}

	// Resolve the session directory via the canonical session file, then list it.
	marker := w.resolveSessionFile(key, session.SessionFileName)
	if marker == "" {
		http.Error(rw, "invalid session key", http.StatusBadRequest)
		return
	}
	dir := filepath.Dir(marker)

	resp := sessionFilesResponse{Key: key, Files: []sessionMarkdownFile{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No directory yet (e.g. brand-new session) → empty list, not an error.
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(resp)
		return
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		resp.Files = append(resp.Files, sessionMarkdownFile{Name: e.Name(), Content: string(content)})
	}
	sort.Slice(resp.Files, func(i, j int) bool { return resp.Files[i].Name < resp.Files[j].Name })

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(resp)
}
