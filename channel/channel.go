// Package channel provides messaging channel interfaces and implementations.
package channel

import (
	"context"
	"fmt"
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

// MessageOrigin holds routing info for the current message, stored in context.
type MessageOrigin struct {
	Channel    string // Channel name (e.g., "telegram")
	ReplyTo    string // Chat/user ID to reply to
	SessionKey string // Session key for pending result isolation
}

type ctxKeyOrigin struct{}

// WithOrigin returns a context with the message origin attached.
func WithOrigin(ctx context.Context, origin MessageOrigin) context.Context {
	return context.WithValue(ctx, ctxKeyOrigin{}, origin)
}

// GetOrigin returns the message origin from the context, if present.
func GetOrigin(ctx context.Context) (MessageOrigin, bool) {
	o, ok := ctx.Value(ctxKeyOrigin{}).(MessageOrigin)
	return o, ok
}

// Handler is a function that processes incoming messages.
type Handler func(ctx context.Context, msg *Message) (*Response, error)

// Router routes messages to handlers based on channel and user.
type Router struct {
	defaultHandler Handler
	handlers       map[string]Handler // ChannelID -> Handler
}

// NewRouter creates a new message router.
func NewRouter(defaultHandler Handler) *Router {
	return &Router{
		defaultHandler: defaultHandler,
		handlers:       make(map[string]Handler),
	}
}

// Register registers a handler for a specific channel.
func (r *Router) Register(channelID string, handler Handler) {
	r.handlers[channelID] = handler
}

// Handle processes a message through the appropriate handler.
func (r *Router) Handle(ctx context.Context, msg *Message) (*Response, error) {
	handler := r.handlers[msg.ChannelID]
	if handler == nil {
		handler = r.defaultHandler
	}
	if handler == nil {
		return nil, nil
	}
	return handler(ctx, msg)
}

// Manager manages multiple channels.
type Manager struct {
	channels map[string]Channel
	router   *Router
}

// NewManager creates a new channel manager.
func NewManager(router *Router) *Manager {
	return &Manager{
		channels: make(map[string]Channel),
		router:   router,
	}
}

// Register adds a channel to the manager.
func (m *Manager) Register(ch Channel) {
	m.channels[ch.Name()] = ch
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
	for _, ch := range m.channels {
		if err := ch.Start(ctx); err != nil {
			return err
		}

		// Start message processing goroutine for this channel
		go m.processMessages(ctx, ch)
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

// processMessages handles incoming messages from a channel.
func (m *Manager) processMessages(ctx context.Context, ch Channel) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch.Messages():
			if !ok {
				return
			}

			// Process through router
			resp, err := m.router.Handle(ctx, msg)
			if err != nil {
				// Log error but continue processing
				continue
			}

			if resp != nil {
				// Send response back through the channel
				_ = ch.Send(ctx, resp)
			}
		}
	}
}
