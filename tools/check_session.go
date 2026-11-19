package tools

import (
	"context"
	"encoding/json"
	"github.com/linanwx/nagobot/prompts"
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
	SessionKey    string      `json:"session_key"`
	Exists        bool        `json:"exists"` // session.jsonl exists on disk
	SessionDir    string      `json:"session_dir,omitempty"`
	Agent         string      `json:"agent,omitempty"` // from meta.json
	MessageCount  int         `json:"message_count,omitempty"`
	FileSizeBytes int64       `json:"file_size_bytes,omitempty"`
	LastModified  time.Time   `json:"last_modified,omitempty"`
	ThreadActive  bool        `json:"thread_active"`    // thread is currently in memory
	Thread        *ThreadInfo `json:"thread,omitempty"` // populated only when ThreadActive
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
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_key": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"session_key"},
	}
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "check_session",
			Description: promptToolCheckSession.Describe(params, prompts.Vars{"TASK_ID_TARGETS": targetsAccepting("task_id")}),
			Parameters:  params,
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
			"session_key":   key,
			"exists":        false,
			"thread_active": false,
		}, promptCheckSessionMissing.Text())
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
			hint = promptCheckSessionRunning.Text()
		case "pending":
			hint = promptCheckSessionPending.Text()
		case "idle":
			hint = promptCheckSessionIdle.Text()
		}
	} else {
		hint = promptCheckSessionUnloaded.Text()
	}

	return toolResult("check_session", fields, hint)
}
