package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/linanwx/nagobot/logger"
)

const socketMessageBufferSize = 100

// socketInbound is the JSON message sent by a CLI client.
type socketInbound struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// SocketOutbound is the JSON message sent to a CLI client.
type SocketOutbound struct {
	Type  string `json:"type"`            // "content" or "error"
	Text  string `json:"text,omitempty"`
	Final bool   `json:"final"`
}

// SocketChannel implements the Channel interface over a unix domain socket.
// The daemon listens; CLI clients connect via `nagobot cli`.
type SocketChannel struct {
	socketPath string
	listener   net.Listener
	messages   chan *Message
	done       chan struct{}
	wg         sync.WaitGroup

	mu      sync.RWMutex
	clients map[string]*socketClient // sessionID → latest client
	peers   map[*socketClient]struct{}
	msgID   atomic.Int64
	stopOnce sync.Once
}

type socketClient struct {
	conn    net.Conn
	encoder *json.Encoder
	mu      sync.Mutex
}

// NewSocketChannel creates a new unix socket channel.
func NewSocketChannel(socketPath string) *SocketChannel {
	return &SocketChannel{
		socketPath: socketPath,
		messages:   make(chan *Message, socketMessageBufferSize),
		done:       make(chan struct{}),
		clients:    make(map[string]*socketClient),
		peers:      make(map[*socketClient]struct{}),
	}
}

func (s *SocketChannel) Name() string { return "socket" }

func (s *SocketChannel) Start(_ /* ctx */ context.Context) error {
	// Clean up stale socket file.
	if _, err := os.Stat(s.socketPath); err == nil {
		// Try connecting to see if another daemon is listening.
		conn, err := net.Dial("unix", s.socketPath)
		if err == nil {
			conn.Close()
			return fmt.Errorf("another daemon is already listening on %s", s.socketPath)
		}
		// Stale socket — remove it.
		os.Remove(s.socketPath)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.socketPath, err)
	}
	s.listener = ln

	s.wg.Add(1)
	go s.acceptLoop()

	logger.Info("socket channel started", "path", s.socketPath)
	return nil
}

func (s *SocketChannel) Stop() error {
	s.stopOnce.Do(func() {
		close(s.done)
		if s.listener != nil {
			s.listener.Close()
		}
		// Close all client connections.
		s.mu.Lock()
		for client := range s.peers {
			client.conn.Close()
		}
		s.mu.Unlock()

		s.wg.Wait()
		close(s.messages)
		os.Remove(s.socketPath)
		logger.Info("socket channel stopped")
	})
	return nil
}

func (s *SocketChannel) Send(_ /* ctx */ context.Context, resp *Response) error {
	if resp == nil {
		return nil
	}

	sessionID := resp.ReplyTo
	if sessionID == "" {
		sessionID = "cli"
	}

	s.mu.RLock()
	client := s.clients[sessionID]
	s.mu.RUnlock()
	if client == nil {
		// Broadcast to all peers if no specific session match.
		s.mu.RLock()
		defer s.mu.RUnlock()
		for peer := range s.peers {
			s.sendToClient(peer, resp.Text, true)
		}
		return nil
	}

	return s.sendToClient(client, resp.Text, true)
}

func (s *SocketChannel) sendToClient(client *socketClient, text string, final bool) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.encoder.Encode(SocketOutbound{
		Type:  "content",
		Text:  text,
		Final: final,
	})
}

func (s *SocketChannel) Messages() <-chan *Message { return s.messages }

func (s *SocketChannel) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				logger.Warn("socket accept error", "err", err)
				continue
			}
		}

		client := &socketClient{
			conn:    conn,
			encoder: json.NewEncoder(conn),
		}
		s.registerPeer(client)
		s.bindClient("cli", client)

		s.wg.Add(1)
		go s.handleConn(client)
	}
}

func (s *SocketChannel) handleConn(client *socketClient) {
	defer s.wg.Done()
	defer func() {
		s.unbindClient("cli", client)
		s.unregisterPeer(client)
		client.conn.Close()
	}()

	decoder := json.NewDecoder(client.conn)
	for {
		var req socketInbound
		if err := decoder.Decode(&req); err != nil {
			// Connection closed or read error.
			return
		}

		msgType := req.Type
		if msgType == "" {
			msgType = "message"
		}
		if msgType != "message" {
			s.sendToClient(client, "unsupported message type", true)
			continue
		}

		text := req.Text
		if text == "" {
			continue
		}

		id := s.msgID.Add(1)
		msg := &Message{
			ID:        fmt.Sprintf("socket-%d", id),
			ChannelID: "socket:local",
			UserID:    "local",
			Username:  "cli-user",
			Text:      text,
			Metadata: map[string]string{
				"chat_id": "cli",
			},
		}

		select {
		case s.messages <- msg:
		case <-s.done:
			return
		}
	}
}

func (s *SocketChannel) registerPeer(client *socketClient) {
	s.mu.Lock()
	s.peers[client] = struct{}{}
	s.mu.Unlock()
	logger.Debug("socket client connected", "peers", len(s.peers))
}

func (s *SocketChannel) unregisterPeer(client *socketClient) {
	s.mu.Lock()
	delete(s.peers, client)
	s.mu.Unlock()
	logger.Debug("socket client disconnected")
}

func (s *SocketChannel) bindClient(sessionID string, client *socketClient) {
	s.mu.Lock()
	s.clients[sessionID] = client
	s.mu.Unlock()
}

func (s *SocketChannel) unbindClient(sessionID string, client *socketClient) {
	s.mu.Lock()
	if s.clients[sessionID] == client {
		delete(s.clients, sessionID)
	}
	s.mu.Unlock()
}
