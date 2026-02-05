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
	// Subagent events (the only events with active subscribers)
	EventSubagentCompleted EventType = "subagent.completed"
	EventSubagentError     EventType = "subagent.error"
)

// Event represents a bus event.
type Event struct {
	ID        string          `json:"id"`
	Type      EventType       `json:"type"`
	Source    string          `json:"source"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
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
	}, nil
}

// ParseData unmarshals the event data into the given struct.
func (e *Event) ParseData(v any) error {
	if e.Data == nil {
		return nil
	}
	return json.Unmarshal(e.Data, v)
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

var eventCounter atomic.Int64

func generateEventID() string {
	n := eventCounter.Add(1)
	return fmt.Sprintf("evt-%d-%d", time.Now().UnixMilli(), n)
}
