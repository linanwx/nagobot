// Package channel provides messaging channel interfaces and implementations.
package channel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/linanwx/nagobot/config"
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

// Reconfigurable is an optional interface for channels that support
// live config updates (e.g. allowedIDs changes without restart).
type Reconfigurable interface {
	Reconfigure(cfg *config.Config)
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
	mu       sync.RWMutex
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
	m.mu.Lock()
	m.channels[ch.Name()] = ch
	m.mu.Unlock()
	logger.Info("channel registered", "channel", ch.Name())
}

// Unregister stops and removes a channel by name.
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	ch, ok := m.channels[name]
	if ok {
		delete(m.channels, name)
	}
	m.mu.Unlock()
	if ok {
		_ = ch.Stop()
		logger.Info("channel unregistered", "channel", name)
	}
}

// Has returns whether a channel with the given name is registered.
func (m *Manager) Has(name string) bool {
	m.mu.RLock()
	_, ok := m.channels[name]
	m.mu.RUnlock()
	return ok
}

// Get returns a channel by name.
func (m *Manager) Get(name string) (Channel, bool) {
	m.mu.RLock()
	ch, ok := m.channels[name]
	m.mu.RUnlock()
	return ch, ok
}

// SendTo sends a text message to a named channel.
func (m *Manager) SendTo(ctx context.Context, channelName, text, replyTo string) error {
	m.mu.RLock()
	ch, ok := m.channels[channelName]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel not found: %s", channelName)
	}
	return ch.Send(ctx, &Response{Text: text, ReplyTo: replyTo})
}

// StartAll starts all registered channels.
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	// Start channels in a deterministic order: socket first, then web, telegram, feishu, then rest.
	ordered := []string{"socket", "web", "telegram", "feishu"}
	started := make(map[string]bool)
	var toStart []Channel
	for _, name := range ordered {
		ch, ok := m.channels[name]
		if !ok {
			continue
		}
		toStart = append(toStart, ch)
		started[name] = true
	}
	for name, ch := range m.channels {
		if started[name] {
			continue
		}
		toStart = append(toStart, ch)
	}
	m.mu.RUnlock()

	for _, ch := range toStart {
		if err := ch.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// StopAll stops all registered channels.
func (m *Manager) StopAll() error {
	m.mu.RLock()
	channels := make([]Channel, 0, len(m.channels))
	for _, ch := range m.channels {
		channels = append(channels, ch)
	}
	m.mu.RUnlock()
	for _, ch := range channels {
		if err := ch.Stop(); err != nil {
			return err
		}
	}
	return nil
}

// Each iterates over all registered channels.
func (m *Manager) Each(fn func(Channel)) {
	m.mu.RLock()
	channels := make([]Channel, 0, len(m.channels))
	for _, ch := range m.channels {
		channels = append(channels, ch)
	}
	m.mu.RUnlock()
	for _, ch := range channels {
		fn(ch)
	}
}

// MediaSummary builds a formatted media summary string for LLM consumption.
// kvs are alternating key-value pairs; empty values are skipped.
func MediaSummary(mediaType string, kvs ...string) string {
	parts := []string{fmt.Sprintf("[Media: %s]", mediaType)}
	for i := 0; i+1 < len(kvs); i += 2 {
		if kvs[i+1] != "" {
			parts = append(parts, kvs[i]+": "+kvs[i+1])
		}
	}
	return strings.Join(parts, "\n")
}

// fmtSeconds formats seconds as "Ns" for MediaSummary; returns "" for zero.
func fmtSeconds(s int) string {
	if s > 0 {
		return fmt.Sprintf("%ds", s)
	}
	return ""
}

// SplitMessage splits a long message into chunks (byte-based maxLen),
// preferring newline boundaries and avoiding mid-rune splits.
func SplitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to split at newline within the byte window.
		splitAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			splitAt = idx + 1
		}

		// Avoid splitting in the middle of a multi-byte UTF-8 character.
		for splitAt > 0 && !utf8.RuneStart(text[splitAt]) {
			splitAt--
		}
		if splitAt == 0 {
			// Entire prefix is a continuation byte sequence; advance past the rune.
			_, size := utf8.DecodeRuneInString(text)
			splitAt = size
		}

		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}

	return chunks
}
