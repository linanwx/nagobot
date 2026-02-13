// Package msg defines the WakeMessage type shared between thread and tools.
package msg

import "context"

// Sink defines how thread output is delivered.
type Sink struct {
	Label string
	Send  func(ctx context.Context, response string) error
}

// IsZero reports whether the sink has no delivery function.
func (s Sink) IsZero() bool { return s.Send == nil }

// ToolCallRecord records a single tool invocation during a turn.
type ToolCallRecord struct {
	Name          string `json:"name"`
	ArgsSummary   string `json:"args"`              // first 200 chars of arguments JSON
	ResultPreview string `json:"result"`            // first 200 chars of tool result
	DurationMs    int64  `json:"durationMs"`        // execution time in milliseconds
	Error         bool   `json:"error,omitempty"`
}

// ThreadInfo holds the summary status of a thread.
type ThreadInfo struct {
	ID         string `json:"id"`
	SessionKey string `json:"sessionKey"`
	State      string `json:"state"`   // "running", "pending", "idle"
	Pending    int    `json:"pending"`
	// Runtime metrics (only populated when state=running).
	Iterations     int              `json:"iterations,omitempty"`
	TotalToolCalls int              `json:"totalToolCalls,omitempty"`
	CurrentTool    string           `json:"currentTool,omitempty"`
	ElapsedSec     int              `json:"elapsedSec,omitempty"`
	ToolTrace      []ToolCallRecord `json:"toolTrace,omitempty"`
}

// WakeMessage is an item in a thread's wake queue.
type WakeMessage struct {
	Source    string            // Wake source: "telegram", "cron", "child_completed", etc.
	Message  string            // Wake payload text.
	Sink     Sink              // Per-wake sink. Zero value = no per-wake delivery.
	AgentName string           // Optional agent name override for this wake.
	Vars     map[string]string // Optional vars override for this wake.
}
