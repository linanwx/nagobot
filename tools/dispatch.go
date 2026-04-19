package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/linanwx/nagobot/provider"
)

// DispatchTarget is the tagged-union discriminator for DispatchSend.
type DispatchTarget string

const (
	TargetCaller   DispatchTarget = "caller"
	TargetSubagent DispatchTarget = "subagent"
	TargetFork     DispatchTarget = "fork"
	TargetSession  DispatchTarget = "session"
)

// DispatchSend is a single dispatch entry. Field requirements vary by To.
type DispatchSend struct {
	To         DispatchTarget `json:"to"`
	Body       string         `json:"body"`
	Agent      string         `json:"agent,omitempty"`       // subagent/fork
	TaskID     string         `json:"task_id,omitempty"`     // subagent/fork
	SessionKey string         `json:"session_key,omitempty"` // session
}

// DispatchHost abstracts the thread-side operations dispatch needs.
type DispatchHost interface {
	CurrentSessionKey() string
	// CallerSessionKey returns the session key of the current wake's caller
	// when addressable; empty for system sources (cron/heartbeat/child/compression).
	CallerSessionKey() string
	AgentExists(name string) bool
	SessionExists(key string) bool
	SendToCaller(ctx context.Context, body string) error
	CreateOrWakeSubagent(ctx context.Context, agent, taskID, body string) (sessionKey, note string, err error)
	CreateOrWakeFork(ctx context.Context, agent, taskID, body string) (sessionKey, note string, err error)
	WakeSession(ctx context.Context, sessionKey, body string) error
	SignalHalt()
}

// DispatchTool is the unified turn-terminating routing primitive.
type DispatchTool struct {
	host DispatchHost
}

// NewDispatchTool creates a dispatch tool bound to the given host.
func NewDispatchTool(host DispatchHost) *DispatchTool {
	return &DispatchTool{host: host}
}

// Def returns the tool definition.
func (t *DispatchTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "dispatch",
			Description: "Turn-terminating routing primitive. Call this at the end of every turn to declare where output goes. " +
				"Each entry in `sends` has a `to` field selecting the target:\n" +
				"- caller: reply to whoever woke this thread. Fields: body.\n" +
				"- subagent: spawn a new subagent thread, or wake existing at same task_id. Fields: agent (optional), task_id, body.\n" +
				"- fork: branch current session as new agent thread, or wake existing at same task_id. Fields: agent (optional), task_id, body.\n" +
				"- session: wake an existing session. Fields: session_key, body.\n\n" +
				"Empty sends — dispatch({}) — is silent turn termination; nothing delivered, history recorded. " +
				"On success the turn ends. On validation error the turn continues — fix and re-call. " +
				"Scheduling is not supported here; use cron for future wakes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sends": map[string]any{
						"type":        "array",
						"description": "List of dispatch entries. Empty or omitted means silent termination.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"to": map[string]any{
									"type":        "string",
									"enum":        []string{"caller", "subagent", "fork", "session"},
									"description": "Target kind.",
								},
								"body": map[string]any{
									"type":        "string",
									"description": "Message body delivered to the target (or injected as wake message).",
								},
								"agent": map[string]any{
									"type":        "string",
									"description": "Agent template name for subagent/fork. Optional — empty falls back to session default.",
								},
								"task_id": map[string]any{
									"type":        "string",
									"description": "Task id for subagent/fork. Must match [a-z0-9_-]+. Reusing the same task_id targets the existing session.",
								},
								"session_key": map[string]any{
									"type":        "string",
									"description": "Existing session key for to=session.",
								},
							},
							"required": []string{"to", "body"},
						},
					},
				},
			},
		},
	}
}

var taskIDRegex = regexp.MustCompile(`^[a-z0-9_-]+$`)

type dispatchArgs struct {
	Sends []DispatchSend `json:"sends"`
}

// ExecutedItem describes a single dispatch entry that was executed.
type ExecutedItem struct {
	To         DispatchTarget `json:"to"`
	SessionKey string         `json:"session_key,omitempty"`
	Note       string         `json:"note,omitempty"`
}

// DispatchError describes a single validation or execution failure.
type DispatchError struct {
	Index  int    `json:"index"`
	To     string `json:"to,omitempty"`
	Detail string `json:"detail"`
}

// Run executes the tool.
func (t *DispatchTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "dispatch", threadToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *DispatchTool) run(ctx context.Context, args json.RawMessage) string {
	var a dispatchArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}
	if t.host == nil {
		return toolError("dispatch", "host not configured")
	}

	// Empty sends → silent turn termination.
	if len(a.Sends) == 0 {
		t.host.SignalHalt()
		return toolResult("dispatch", map[string]any{
			"executed": []any{},
			"outcome":  "turn-terminated-silent",
		}, "Turn terminated silently. No delivery; history recorded.")
	}

	// Validate entire batch first (all-or-nothing on validation).
	if errs := t.validateAll(a.Sends); len(errs) > 0 {
		return buildDispatchErrorResult(errs)
	}

	// Execute. Partial failure possible — SignalHalt either way.
	executed := make([]ExecutedItem, 0, len(a.Sends))
	var execErrs []DispatchError
	for i, send := range a.Sends {
		item, err := t.execute(ctx, send)
		if err != nil {
			execErrs = append(execErrs, DispatchError{
				Index:  i,
				To:     string(send.To),
				Detail: err.Error(),
			})
			continue
		}
		executed = append(executed, item)
	}

	t.host.SignalHalt()
	if len(execErrs) > 0 {
		return buildDispatchMixedResult(executed, execErrs)
	}
	return buildDispatchSuccessResult(executed)
}

// validateAll performs all static, existence, and dedup checks.
func (t *DispatchTool) validateAll(sends []DispatchSend) []DispatchError {
	var errs []DispatchError
	currentSession := t.host.CurrentSessionKey()
	keysInBatch := map[string]int{}

	for i, send := range sends {
		if detail := t.validateOne(send, currentSession); detail != "" {
			errs = append(errs, DispatchError{Index: i, To: string(send.To), Detail: detail})
			continue
		}
		key := targetKey(send, currentSession)
		if key == "" {
			continue
		}
		if _, dup := keysInBatch[key]; dup {
			errs = append(errs, DispatchError{
				Index:  i,
				To:     string(send.To),
				Detail: fmt.Sprintf("duplicate target in batch: %s", key),
			})
			continue
		}
		keysInBatch[key] = i
	}
	return errs
}

func (t *DispatchTool) validateOne(send DispatchSend, currentSession string) string {
	if strings.TrimSpace(send.Body) == "" {
		return "body is required"
	}
	switch send.To {
	case TargetCaller:
		if send.Agent != "" || send.TaskID != "" || send.SessionKey != "" {
			return "caller does not accept agent/task_id/session_key"
		}
		if t.host.CallerSessionKey() == "" {
			return "current wake has no routable caller (system source like cron/heartbeat/child)"
		}
	case TargetSubagent, TargetFork:
		if send.SessionKey != "" {
			return fmt.Sprintf("%s does not accept session_key", send.To)
		}
		if strings.TrimSpace(send.TaskID) == "" {
			return "task_id is required"
		}
		if !taskIDRegex.MatchString(send.TaskID) {
			return "task_id must match [a-z0-9_-]+"
		}
		if send.Agent != "" && !t.host.AgentExists(send.Agent) {
			return fmt.Sprintf("agent %q not found", send.Agent)
		}
	case TargetSession:
		if send.Agent != "" || send.TaskID != "" {
			return "session does not accept agent/task_id"
		}
		if strings.TrimSpace(send.SessionKey) == "" {
			return "session_key is required"
		}
		if send.SessionKey == currentSession {
			return "session_key cannot be the current session (self-reference not allowed)"
		}
		if !t.host.SessionExists(send.SessionKey) {
			return fmt.Sprintf("session %q not found", send.SessionKey)
		}
	default:
		return fmt.Sprintf("unknown to: %q (must be one of caller/subagent/fork/session)", send.To)
	}
	return ""
}

// targetKey returns a stable string identifying the resolved target, for batch dedup.
func targetKey(send DispatchSend, currentSession string) string {
	switch send.To {
	case TargetCaller:
		return "caller" // at most one caller per batch
	case TargetSubagent:
		return currentSession + ":threads:" + send.TaskID
	case TargetFork:
		return currentSession + ":fork:" + send.TaskID
	case TargetSession:
		return send.SessionKey
	}
	return ""
}

// execute performs a single dispatch against the host.
func (t *DispatchTool) execute(ctx context.Context, send DispatchSend) (ExecutedItem, error) {
	switch send.To {
	case TargetCaller:
		if err := t.host.SendToCaller(ctx, send.Body); err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetCaller, SessionKey: t.host.CallerSessionKey()}, nil
	case TargetSubagent:
		key, note, err := t.host.CreateOrWakeSubagent(ctx, send.Agent, send.TaskID, send.Body)
		if err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetSubagent, SessionKey: key, Note: note}, nil
	case TargetFork:
		key, note, err := t.host.CreateOrWakeFork(ctx, send.Agent, send.TaskID, send.Body)
		if err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetFork, SessionKey: key, Note: note}, nil
	case TargetSession:
		if err := t.host.WakeSession(ctx, send.SessionKey, send.Body); err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetSession, SessionKey: send.SessionKey}, nil
	}
	return ExecutedItem{}, fmt.Errorf("unknown to: %q", send.To)
}

func buildDispatchErrorResult(errs []DispatchError) string {
	list := make([]any, 0, len(errs))
	for _, e := range errs {
		entry := map[string]any{"index": e.Index, "detail": e.Detail}
		if e.To != "" {
			entry["to"] = e.To
		}
		list = append(list, entry)
	}
	return toolResult("dispatch", map[string]any{
		"errors":  list,
		"outcome": "validation-error",
	}, "Validation failed — no sends executed. Fix errors and re-call dispatch. Turn continues.")
}

func buildDispatchSuccessResult(executed []ExecutedItem) string {
	list := make([]any, 0, len(executed))
	for _, ex := range executed {
		entry := map[string]any{"to": string(ex.To), "session_key": ex.SessionKey}
		if ex.Note != "" {
			entry["note"] = ex.Note
		}
		list = append(list, entry)
	}
	return toolResult("dispatch", map[string]any{
		"executed": list,
		"outcome":  "turn-terminated",
	}, "All sends executed. Turn ended.")
}

func buildDispatchMixedResult(executed []ExecutedItem, errs []DispatchError) string {
	exList := make([]any, 0, len(executed))
	for _, ex := range executed {
		entry := map[string]any{"to": string(ex.To), "session_key": ex.SessionKey}
		if ex.Note != "" {
			entry["note"] = ex.Note
		}
		exList = append(exList, entry)
	}
	errList := make([]any, 0, len(errs))
	for _, e := range errs {
		entry := map[string]any{"index": e.Index, "detail": e.Detail}
		if e.To != "" {
			entry["to"] = e.To
		}
		errList = append(errList, entry)
	}
	return toolResult("dispatch", map[string]any{
		"executed": exList,
		"errors":   errList,
		"outcome":  "partial-failure",
	}, "Some sends succeeded, some failed. Turn ended — executed deliveries cannot be unrolled.")
}
