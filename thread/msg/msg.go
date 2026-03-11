// Package msg defines the WakeMessage type shared between thread and tools.
package msg

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// BuildSystemMessage constructs a standardized XML system message.
// Fields are rendered as child elements in sorted order; content goes into
// a <message visibility="assistant-only"> element.
func BuildSystemMessage(msgType string, fields map[string]string, content string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<system type=%q>\n", msgType)

	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "  <%s>%s</%s>\n", k, fields[k], k)
		}
	}

	content = strings.TrimSpace(content)
	if content != "" {
		fmt.Fprintf(&sb, "  <message visibility=\"assistant-only\">%s</message>\n", content)
	}

	sb.WriteString("</system>")
	return sb.String()
}

// Sink defines how thread output is delivered.
type Sink struct {
	Label      string
	Send       func(ctx context.Context, response string) error
	Idempotent bool // True for display-only sinks (telegram, feishu, cli) safe for intermediate streaming.
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

// WakeSource identifies how a thread was woken.
type WakeSource string

const (
	WakeTelegram       WakeSource = "telegram"
	WakeCLI            WakeSource = "cli"
	WakeWeb            WakeSource = "web"
	WakeDiscord        WakeSource = "discord"
	WakeFeishu         WakeSource = "feishu"
	WakeUserActive     WakeSource = "user_active"
	WakeChildTask      WakeSource = "child_task"
	WakeChildCompleted WakeSource = "child_completed"
	WakeSleepCompleted WakeSource = "sleep_completed"
	WakeCron           WakeSource = "cron"
	WakeCronFinished   WakeSource = "cron_finished"
	WakeExternal       WakeSource = "external"
	WakeCompression      WakeSource = "compression"
	WakeHeartbeatReflect WakeSource = "heartbeat_reflect"
	WakeHeartbeatWake    WakeSource = "heartbeat_wake"
)

// WakeMessage is an item in a thread's wake queue.
type WakeMessage struct {
	Source    WakeSource        // Wake source.
	Message  string            // Wake payload text.
	Sink     Sink              // Per-wake sink. Zero value = no per-wake delivery.
	AgentName string           // Optional agent name override for this wake.
	Vars     map[string]string // Optional vars override for this wake.
}
