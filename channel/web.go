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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

const (
	webMainSessionID     = "cli"
	webMessageBufferSize = 100
	webDefaultAddr       = "127.0.0.1:18080"
	webShutdownTimeout   = 5 * time.Second
	sessionsDirName      = "sessions"
)

//go:embed web/dist/*
var rawFrontendFS embed.FS

// WebChannel implements the Channel interface for browser chat.
type WebChannel struct {
	addr      string
	workspace string
	messages  chan *Message
	done      chan struct{}
	wg        sync.WaitGroup
	server    *http.Server

	mu       sync.RWMutex
	clients  map[string]*wsClient
	peers    map[*wsClient]struct{}
	msgID    int64
	stopOnce sync.Once

	systemPromptFn  func(string) (string, bool)
	toolDefsFn      func(string) ([]provider.ToolDef, bool)
	contextBudgetFn func(string) (int, int, bool)
}

type wsClient struct {
	conn         *websocket.Conn
	mu           sync.Mutex
	boundSession string // session key this client is bound to
}

type webInboundMessage struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Text      string `json:"text"`
}

type webOutboundMessage struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// NewWebChannel creates a new web channel from config.
func NewWebChannel(cfg *config.Config) Channel {
	addr := cfg.GetWebAddr()
	if addr == "" {
		addr = webDefaultAddr
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		logger.Warn("web channel: failed to get workspace path", "err", err)
	}

	return &WebChannel{
		addr:      addr,
		workspace: workspace,
		messages:  make(chan *Message, webMessageBufferSize),
		done:      make(chan struct{}),
		clients:   make(map[string]*wsClient),
		peers:     make(map[*wsClient]struct{}),
	}
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

// Name returns the channel name.
func (w *WebChannel) Name() string { return "web" }

// Start starts the web server.
func (w *WebChannel) Start(ctx context.Context) error {
	frontendFS, err := fs.Sub(rawFrontendFS, "web/dist")
	if err != nil {
		return fmt.Errorf("failed to load embedded web frontend: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", http.HandlerFunc(w.handleWS))
	mux.Handle("/api/history", http.HandlerFunc(w.handleHistory))
	mux.Handle("/api/sessions/", http.HandlerFunc(w.handleSessionMessages))
	mux.Handle("/api/sessions", http.HandlerFunc(w.handleSessions))
	mux.Handle("/api/config", http.HandlerFunc(w.handleConfig))
	mux.Handle("/api/heartbeat/", http.HandlerFunc(w.handleHeartbeat))
	mux.Handle("/", http.FileServer(http.FS(frontendFS)))

	w.server = &http.Server{
		Addr:    w.addr,
		Handler: mux,
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
		w.clients = make(map[string]*wsClient)
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

	w.mu.RLock()
	client := w.clients[sessionID]
	w.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("web session not connected: %s", sessionID)
	}

	payload := webOutboundMessage{
		Type: "response",
		Text: resp.Text,
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if err := wsjson.Write(ctx, client.conn, payload); err != nil {
		return fmt.Errorf("websocket send failed: %w", err)
	}
	return nil
}

// Messages returns the incoming message channel.
func (w *WebChannel) Messages() <-chan *Message { return w.messages }

func (w *WebChannel) handleWS(rw http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(rw, r, nil)
	if err != nil {
		return
	}

	client := &wsClient{conn: conn, boundSession: webMainSessionID}
	w.registerPeer(client)
	w.bindClient(webMainSessionID, client)

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
			if text == "" {
				continue
			}

			client.mu.Lock()
			boundSess := client.boundSession
			client.mu.Unlock()

			sessionID := boundSess
			channelID := "web:" + sessionID
			if sid := strings.TrimSpace(req.SessionID); sid != "" {
				if valid := sanitizeSessionKey(sid); valid != "" {
					sessionID = valid
					channelID = "web:" + valid
				}
			}

			msg := &Message{
				ID:        fmt.Sprintf("web-%d", atomic.AddInt64(&w.msgID, 1)),
				ChannelID: channelID,
				UserID:    sessionID,
				Username:  "web-user",
				Text:      text,
				Metadata: map[string]string{
					"chat_id": sessionID,
				},
			}

			select {
			case w.messages <- msg:
			case <-w.done:
				return
			case <-r.Context().Done():
				return
			}

		default:
			_ = wsjson.Write(r.Context(), conn, webOutboundMessage{Type: "error", Error: "unsupported message type"})
		}
	}
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
	w.mu.Lock()
	old := w.clients[sessionID]
	w.clients[sessionID] = client
	w.mu.Unlock()

	if old != nil && old != client {
		_ = old.conn.Close(websocket.StatusNormalClosure, "replaced")
	}
}

func (w *WebChannel) unbindClient(sessionID string, client *wsClient) {
	w.mu.Lock()
	defer w.mu.Unlock()
	current := w.clients[sessionID]
	if current == client {
		delete(w.clients, sessionID)
	}
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
		contextWindow = provider.EffectiveContextWindow(cfg.GetModelName(), cfg.GetContextWindowTokens())
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

	redactConfig(cfg)

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(cfg)
}

const redactedValue = "***configured***"

// redactConfig replaces sensitive fields with a placeholder.
func redactConfig(cfg *config.Config) {
	redactProvider := func(pc *config.ProviderConfig) {
		if pc != nil && pc.APIKey != "" {
			pc.APIKey = redactedValue
		}
	}
	redactProvider(cfg.Providers.OpenRouter)
	redactProvider(cfg.Providers.Anthropic)
	redactProvider(cfg.Providers.DeepSeek)
	redactProvider(cfg.Providers.MoonshotCN)
	redactProvider(cfg.Providers.MoonshotGlobal)
	redactProvider(cfg.Providers.ZhipuCN)
	redactProvider(cfg.Providers.ZhipuGlobal)
	redactProvider(cfg.Providers.MinimaxCN)
	redactProvider(cfg.Providers.MinimaxGlobal)
	redactProvider(cfg.Providers.OpenAI)
	redactProvider(cfg.Providers.Gemini)

	if cfg.Providers.OpenAIOAuth != nil {
		if cfg.Providers.OpenAIOAuth.AccessToken != "" {
			cfg.Providers.OpenAIOAuth.AccessToken = redactedValue
		}
		if cfg.Providers.OpenAIOAuth.RefreshToken != "" {
			cfg.Providers.OpenAIOAuth.RefreshToken = redactedValue
		}
	}

	// Redact channel tokens.
	if cfg.Channels != nil {
		if cfg.Channels.Telegram != nil && cfg.Channels.Telegram.Token != "" {
			cfg.Channels.Telegram.Token = redactedValue
		}
		if cfg.Channels.Discord != nil && cfg.Channels.Discord.Token != "" {
			cfg.Channels.Discord.Token = redactedValue
		}
		if cfg.Channels.Feishu != nil {
			if cfg.Channels.Feishu.AppSecret != "" {
				cfg.Channels.Feishu.AppSecret = redactedValue
			}
		}
	}

	// Redact tool keys.
	if cfg.Tools.Web.Fetch.JinaKey != "" {
		cfg.Tools.Web.Fetch.JinaKey = redactedValue
	}
	for k, v := range cfg.Tools.Web.Search.Keys {
		if v != "" {
			cfg.Tools.Web.Search.Keys[k] = redactedValue
		}
	}
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
