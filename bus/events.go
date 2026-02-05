// Package bus provides event bus and subagent management.
package bus

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

// EventType represents the type of event.
type EventType string

const (
	// Agent lifecycle events
	EventAgentStarted  EventType = "agent.started"
	EventAgentStopped  EventType = "agent.stopped"
	EventAgentError    EventType = "agent.error"

	// Message events
	EventMessageReceived EventType = "message.received"
	EventMessageSent     EventType = "message.sent"

	// Tool events
	EventToolCalled    EventType = "tool.called"
	EventToolCompleted EventType = "tool.completed"
	EventToolError     EventType = "tool.error"

	// Subagent events
	EventSubagentSpawned    EventType = "subagent.spawned"
	EventSubagentCompleted  EventType = "subagent.completed"
	EventSubagentError      EventType = "subagent.error"

	// Memory events
	EventMemoryUpdated EventType = "memory.updated"

	// Custom events
	EventCustom EventType = "custom"
)

// Event represents a bus event.
type Event struct {
	ID        string          `json:"id"`
	Type      EventType       `json:"type"`
	Source    string          `json:"source"`    // Agent ID or component name
	Target    string          `json:"target"`    // Optional target agent ID
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
}

// NewEvent creates a new event.
func NewEvent(eventType EventType, source string, data any) (*Event, error) {
	var dataBytes json.RawMessage
	if data != nil {
		var err error
		dataBytes, err = json.Marshal(data)
		if err != nil {
			return nil, err
		}
	}

	return &Event{
		ID:        generateEventID(),
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now(),
		Data:      dataBytes,
		Metadata:  make(map[string]any),
	}, nil
}

// WithTarget sets the target for the event.
func (e *Event) WithTarget(target string) *Event {
	e.Target = target
	return e
}

// WithMetadata adds metadata to the event.
func (e *Event) WithMetadata(key string, value any) *Event {
	if e.Metadata == nil {
		e.Metadata = make(map[string]any)
	}
	e.Metadata[key] = value
	return e
}

// ParseData unmarshals the event data into the given struct.
func (e *Event) ParseData(v any) error {
	if e.Data == nil {
		return nil
	}
	return json.Unmarshal(e.Data, v)
}

// ============================================================================
// Common Event Data Types
// ============================================================================

// MessageEventData contains data for message events.
type MessageEventData struct {
	Content   string `json:"content"`
	Role      string `json:"role"`
	SessionID string `json:"session_id,omitempty"`
}

// ToolEventData contains data for tool events.
type ToolEventData struct {
	ToolName  string          `json:"tool_name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Result    string          `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Duration  time.Duration   `json:"duration,omitempty"`
}

// SubagentEventData contains data for subagent events.
type SubagentEventData struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	Task      string `json:"task,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	// Origin routing info for push delivery of async results.
	OriginChannel    string `json:"origin_channel,omitempty"`
	OriginReplyTo    string `json:"origin_reply_to,omitempty"`
	OriginSessionKey string `json:"origin_session_key,omitempty"`
}

// AgentErrorData contains data for agent error events.
type AgentErrorData struct {
	Error   string `json:"error"`
	Context string `json:"context,omitempty"`
}

// ============================================================================
// Helper Functions
// ============================================================================

var eventCounter atomic.Int64

// generateEventID generates a unique event ID.
func generateEventID() string {
	n := eventCounter.Add(1)
	return fmt.Sprintf("evt-%d-%d", time.Now().UnixMilli(), n)
}
