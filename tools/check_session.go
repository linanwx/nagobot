package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

// ThreadInfo is an alias for msg.ThreadInfo, exposed for consumers that import
// this package (manager/run) without needing to also import thread/msg.
type ThreadInfo = msg.ThreadInfo

// ToolCallRecord is an alias for msg.ToolCallRecord.
type ToolCallRecord = msg.ToolCallRecord

// SessionStatusInfo combines disk-side session metadata with thread state
// (when a thread is currently loaded for the session). All fields are
// optional — population depends on whether the session and/or thread exist.
type SessionStatusInfo struct {
	SessionKey       string     `json:"session_key"`
	Exists           bool       `json:"exists"`                       // session.jsonl exists on disk
	SessionDir       string     `json:"session_dir,omitempty"`
	Agent            string     `json:"agent,omitempty"`              // from meta.json
	MessageCount     int        `json:"message_count,omitempty"`
	FileSizeBytes    int64      `json:"file_size_bytes,omitempty"`
	LastModified     time.Time  `json:"last_modified,omitempty"`
	ThreadActive     bool       `json:"thread_active"`                // thread is currently in memory
	Thread           *ThreadInfo `json:"thread,omitempty"`            // populated only when ThreadActive
}

// SessionChecker is implemented by Manager.
type SessionChecker interface {
	SessionStatus(sessionKey string) SessionStatusInfo
}

// CheckSessionTool inspects a session by key, reporting both disk state and
// the in-memory thread state if any thread is currently loaded.
type CheckSessionTool struct {
	checker SessionChecker
}

// NewCheckSessionTool creates the tool.
func NewCheckSessionTool(checker SessionChecker) *CheckSessionTool {
	return &CheckSessionTool{checker: checker}
}

// Def returns the tool definition.
func (t *CheckSessionTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "check_session",
			Description: "Inspect a session by key. Reports whether the session exists on disk, " +
				"whether a thread is currently loaded for it, and the thread's runtime state " +
				"(iterations / current tool / pending) when a thread is active. " +
				"Use this after dispatch (subagent/fork) to follow up on a child session by its " +
				"resolved session_key.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": map[string]any{
						"type":        "string",
						"description": "Target session key (e.g. 'cli:threads:bg-check', 'telegram:123456').",
					},
				},
				"required": []string{"session_key"},
			},
		},
	}
}

type checkSessionArgs struct {
	SessionKey string `json:"session_key" required:"true"`
}

// Run executes the tool.
func (t *CheckSessionTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "check_session", threadToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *CheckSessionTool) run(_ context.Context, args json.RawMessage) string {
	var a checkSessionArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}
	if t.checker == nil {
		return toolError("check_session", "session checker not configured")
	}

	key := strings.TrimSpace(a.SessionKey)
	if key == "" {
		return toolError("check_session", "session_key is required")
	}

	info := t.checker.SessionStatus(key)

	if !info.Exists && !info.ThreadActive {
		return toolResult("check_session", map[string]any{
			"session_key": key,
			"exists":      false,
			"thread_active": false,
		}, "Session not found on disk and no thread loaded. Either it never existed or both the file and thread have been removed.")
	}

	fields := map[string]any{
		"session_key":   info.SessionKey,
		"exists":        info.Exists,
		"thread_active": info.ThreadActive,
	}
	if info.SessionDir != "" {
		fields["session_dir"] = info.SessionDir
	}
	if info.Agent != "" {
		fields["agent"] = info.Agent
	}
	if info.MessageCount > 0 {
		fields["message_count"] = info.MessageCount
	}
	if info.FileSizeBytes > 0 {
		fields["file_size_bytes"] = info.FileSizeBytes
	}
	if !info.LastModified.IsZero() {
		fields["last_modified"] = info.LastModified.Format(time.RFC3339)
	}

	var hint string
	if info.ThreadActive && info.Thread != nil {
		fields["thread_id"] = info.Thread.ID
		fields["thread_state"] = info.Thread.State
		fields["thread_pending"] = info.Thread.Pending
		if info.Thread.Iterations > 0 {
			fields["thread_iterations"] = info.Thread.Iterations
		}
		if info.Thread.TotalToolCalls > 0 {
			fields["thread_total_tool_calls"] = info.Thread.TotalToolCalls
		}
		if info.Thread.CurrentTool != "" {
			fields["thread_current_tool"] = info.Thread.CurrentTool
		}
		if info.Thread.ElapsedSec > 0 {
			fields["thread_elapsed_sec"] = info.Thread.ElapsedSec
		}
		switch info.Thread.State {
		case "running":
			hint = "Thread is running. It will deliver output via its sink when done. " +
				"Wait for the result rather than polling — sleep your turn or do other work."
		case "pending":
			hint = "Thread has queued messages but is not currently executing. The Manager will pick it up shortly."
		case "idle":
			hint = "Thread is loaded and idle. No pending work."
		}
	} else {
		hint = "Session exists on disk but no thread is currently loaded. " +
			"A new thread will be created automatically the next time the session is woken " +
			"(via dispatch with to=session, or any other wake source)."
	}

	return toolResult("check_session", fields, hint)
}
