// Package channel provides messaging channel interfaces and implementations.
package channel

import (
	"context"
	"fmt"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// Message represents an incoming message from a channel.
type Message struct {
	ID        string            // Unique message ID
	ChannelID string            // Channel identifier (e.g., "telegram:123456")
	UserID    string            // User identifier
	Username  string            // Human-readable username
	Text      string            // Message text
	ReplyTo   string            // ID of message being replied to (if any)
	Metadata  map[string]string // Channel-specific metadata
}

// Response represents a response to send back.
type Response struct {
	Text     string            // Response text
	ReplyTo  string            // Message ID to reply to
	Metadata map[string]string // Channel-specific options
}

// Channel is the interface for messaging channels.
type Channel interface {
	// Name returns the channel name (e.g., "telegram", "cli", "webhook").
	Name() string

	// Start begins listening for messages.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the channel.
	Stop() error

	// Send sends a response message.
	Send(ctx context.Context, resp *Response) error

	// Messages returns a channel for receiving incoming messages.
	Messages() <-chan *Message
}

// Manager manages multiple channels as a pure registry.
type Manager struct {
	channels map[string]Channel
}

// NewManager creates a new channel manager.
func NewManager() *Manager {
	return &Manager{
		channels: make(map[string]Channel),
	}
}

// Register adds a channel to the manager and logs it. Nil is silently ignored.
func (m *Manager) Register(ch Channel) {
	if ch == nil {
		return
	}
	m.channels[ch.Name()] = ch
	logger.Info("channel registered", "channel", ch.Name())
}

// Get returns a channel by name.
func (m *Manager) Get(name string) (Channel, bool) {
	ch, ok := m.channels[name]
	return ch, ok
}

// SendTo sends a text message to a named channel.
func (m *Manager) SendTo(ctx context.Context, channelName, text, replyTo string) error {
	ch, ok := m.channels[channelName]
	if !ok {
		return fmt.Errorf("channel not found: %s", channelName)
	}
	return ch.Send(ctx, &Response{Text: text, ReplyTo: replyTo})
}

// StartAll starts all registered channels.
func (m *Manager) StartAll(ctx context.Context) error {
	if webCh, ok := m.channels["web"]; ok {
		if err := webCh.Start(ctx); err != nil {
			return err
		}
	}

	telegramCh, hasTelegram := m.channels["telegram"]
	if hasTelegram {
		if err := telegramCh.Start(ctx); err != nil {
			return err
		}
	}

	if cliCh, ok := m.channels["cli"]; ok {
		if hasTelegram {
			time.Sleep(1 * time.Second)
		}
		if err := cliCh.Start(ctx); err != nil {
			return err
		}
	}

	// Start any remaining channels not handled above.
	for name, ch := range m.channels {
		if name == "web" || name == "telegram" || name == "cli" {
			continue
		}
		if err := ch.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// StopAll stops all registered channels.
func (m *Manager) StopAll() error {
	for _, ch := range m.channels {
		if err := ch.Stop(); err != nil {
			return err
		}
	}
	return nil
}

// Each iterates over all registered channels.
func (m *Manager) Each(fn func(Channel)) {
	for _, ch := range m.channels {
		fn(ch)
	}
}
