// Package msg defines the WakeMessage type shared between thread and tools.
package msg

import (
	"context"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// BuildSystemMessage constructs a standardized system message using YAML frontmatter.
// Fields are rendered in sorted order; content goes into the markdown body.
func BuildSystemMessage(msgType string, fields map[string]string, content string) string {
	// Build ordered map: type, sender, then sorted fields.
	header := yaml.Node{Kind: yaml.MappingNode}
	addYAMLPair(&header, "type", msgType)
	addYAMLPair(&header, "sender", "system")

	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			addYAMLPair(&header, k, fields[k])
		}
	}

	yamlBytes, _ := yaml.Marshal(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&header}})

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(yamlBytes)
	sb.WriteString("---\n")

	content = strings.TrimSpace(content)
	if content != "" {
		sb.WriteString("\n")
		sb.WriteString(content)
	}

	return sb.String()
}

func addYAMLPair(node *yaml.Node, key, value string) {
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
}

// ReactEvent identifies a lifecycle event for reaction purposes.
type ReactEvent int

const (
	ReactToolCalls ReactEvent = iota // tool call detected
	ReactStreaming                   // first text content generated
)

// ReactFunc wraps a nil-safe reaction callback.
// Each platform maps ReactEvent to its own emoji.
type ReactFunc struct {
	fn func(ctx context.Context, event ReactEvent)
}

// NewReactFunc creates a ReactFunc from a callback.
func NewReactFunc(fn func(ctx context.Context, event ReactEvent)) ReactFunc {
	return ReactFunc{fn: fn}
}

// IsZero reports whether no reaction function is set.
func (r ReactFunc) IsZero() bool { return r.fn == nil }

// Do fires the reaction. Safe to call on zero value.
func (r ReactFunc) Do(ctx context.Context, event ReactEvent) {
	if r.fn != nil {
		r.fn(ctx, event)
	}
}

// Sink defines how thread output is delivered.
type Sink struct {
	Label     string
	Send      func(ctx context.Context, response string) error
	React     ReactFunc // Optional: fire-and-forget emoji reaction on the source message.
	Chunkable bool      // True for sinks that accept chunked streaming delivery (telegram, discord, feishu, cli).
}

// IsZero reports whether the sink has no delivery function.
func (s Sink) IsZero() bool { return s.Send == nil }

// WithoutStreaming returns a copy with Chunkable disabled, suppressing
// streaming deltas and intermediate content delivery while keeping
// final response delivery intact.
func (s Sink) WithoutStreaming() Sink {
	s.Chunkable = false
	return s
}

// WithRetry wraps the sink's Send with exponential-backoff retry logic.
func (s Sink) WithRetry(maxAttempts int) Sink {
	original := s.Send
	s.Send = func(ctx context.Context, response string) error {
		var err error
		for i := 0; i < maxAttempts; i++ {
			if err = original(ctx, response); err == nil {
				return nil
			}
			if i < maxAttempts-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(1<<i) * time.Second):
				}
			}
		}
		return err
	}
	return s
}

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
	LastUserActiveAt time.Time      `json:"lastUserActiveAt,omitempty"`
}

// WakeSource identifies how a thread was woken.
type WakeSource string

const (
	WakeTelegram       WakeSource = "telegram"
	WakeCLI            WakeSource = "cli"
	WakeWeb            WakeSource = "web"
	WakeDiscord        WakeSource = "discord"
	WakeFeishu         WakeSource = "feishu"
	WakeWeCom          WakeSource = "wecom"
	WakeSocket         WakeSource = "socket"
	WakeUserActive     WakeSource = "user_active"
	WakeChildTask      WakeSource = "child_task"
	WakeChildCompleted WakeSource = "child_completed"
	WakeSleepCompleted WakeSource = "sleep_completed"
	WakeCron           WakeSource = "cron"
	WakeCronFinished   WakeSource = "cron_finished"
	WakeExternal       WakeSource = "external"
	WakeCompression      WakeSource = "compression"
	WakeHeartbeat  WakeSource = "heartbeat"
	WakeResume     WakeSource = "resume"
	WakeRephrase   WakeSource = "rephrase"
)

// IsUserVisibleSource reports whether the given source represents a real
// user-initiated channel (telegram, discord, cli, web, feishu).
func IsUserVisibleSource(source WakeSource) bool {
	switch source {
	case WakeTelegram, WakeDiscord, WakeCLI, WakeWeb, WakeFeishu, WakeWeCom, WakeSocket:
		return true
	}
	return false
}

// WakeMessage is an item in a thread's wake queue.
type WakeMessage struct {
	Source     WakeSource        // Wake source.
	Message    string            // Wake payload text.
	Sink       Sink              // Per-wake sink. Zero value = no per-wake delivery.
	AgentName  string            // Optional agent name override for this wake.
	Vars       map[string]string // Optional vars override for this wake.
	Sender     string            // Optional sender override (e.g. rephrase inherits original sender).
	OnComplete func(response string) // Called after the turn completes with the full response text.
}
