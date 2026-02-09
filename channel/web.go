package channel

import (
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const (
	webMainSessionID     = "main"
	webMessageBufferSize = 100
	webDefaultAddr       = "127.0.0.1:8080"
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

	mu      sync.RWMutex
	clients map[string]*wsClient
	peers   map[*wsClient]struct{}
	msgID   int64
}

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
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
	workspace, _ := cfg.WorkspacePath()

	return &WebChannel{
		addr:      addr,
		workspace: workspace,
		messages:  make(chan *Message, webMessageBufferSize),
		done:      make(chan struct{}),
		clients:   make(map[string]*wsClient),
		peers:     make(map[*wsClient]struct{}),
	}
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
	select {
	case <-w.done:
	default:
		close(w.done)
	}

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
	return nil
}

// Send sends a response to the web client.
func (w *WebChannel) Send(ctx context.Context, resp *Response) error {
	if resp == nil {
		return fmt.Errorf("response is nil")
	}

	sessionID := sanitizeSessionID(resp.ReplyTo)
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

	client := &wsClient{conn: conn}
	w.registerPeer(client)
	w.bindClient(webMainSessionID, client)

	w.wg.Add(1)
	defer w.wg.Done()
	defer func() {
		w.unregisterPeer(client)
		w.unbindClient(webMainSessionID, client)
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
		if reqType != "message" {
			_ = wsjson.Write(r.Context(), conn, webOutboundMessage{Type: "error", Error: "unsupported message type"})
			continue
		}

		text := strings.TrimSpace(req.Text)
		if text == "" {
			continue
		}

		msg := &Message{
			ID:        fmt.Sprintf("web-%d", atomic.AddInt64(&w.msgID, 1)),
			ChannelID: "web:main",
			UserID:    webMainSessionID,
			Username:  "web-user",
			Text:      text,
			Metadata: map[string]string{
				"chat_id": webMainSessionID,
			},
		}

		select {
		case w.messages <- msg:
		case <-w.done:
			return
		case <-r.Context().Done():
			return
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

func sanitizeSessionID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 128 {
		return ""
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return ""
	}
	return s
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

type sessionFileEnvelope struct {
	Messages []sessionFileMessage `json:"messages"`
}

type sessionFileMessage struct {
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

	path := filepath.Join(w.workspace, sessionsDirName, "main", "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []webHistoryMessage{}, nil
		}
		return nil, err
	}

	var src sessionFileEnvelope
	if err := json.Unmarshal(data, &src); err != nil {
		return nil, err
	}

	out := make([]webHistoryMessage, 0, len(src.Messages))
	for _, m := range src.Messages {
		role := strings.TrimSpace(m.Role)
		content := strings.TrimSpace(m.Content)
		if role == "" || content == "" {
			continue
		}
		out = append(out, webHistoryMessage{Role: role, Content: content})
	}
	return out, nil
}
