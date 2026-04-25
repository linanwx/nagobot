package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

// DispatchTarget is the tagged-union discriminator for DispatchSend.
type DispatchTarget string

const (
	// TargetCallerUser replies to the caller AND asserts the caller is the
	// channel user. Fails validation if the current wake's caller is another
	// session or a system source (cron/heartbeat/compression).
	TargetCallerUser DispatchTarget = "caller:user"
	// TargetCallerSession replies to the caller AND asserts the caller is
	// another session. Fails validation if the caller is the channel user or
	// a system source.
	TargetCallerSession DispatchTarget = "caller:session"
	TargetUser          DispatchTarget = "user"
	TargetSubagent      DispatchTarget = "subagent"
	TargetFork          DispatchTarget = "fork"
	TargetSession       DispatchTarget = "session"
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
	// CallerInfo returns an atomic snapshot of the current wake's caller:
	// kind — "user" when the caller is the channel user; "session" when the
	//        caller is another session (cross-session wake); "system" when
	//        the caller is cron/heartbeat/compression/resume/rephrase (drop
	//        sinks — any reply to caller is discarded). Empty string means
	//        no active caller (edge case).
	// callerKey — upstream session key when kind=="session", empty otherwise.
	// sinkLabel — human-readable destination shown back to the LLM on
	//             successful caller delivery.
	CallerInfo() (kind msg.CallerKind, callerKey, sinkLabel string)
	// IsUserFacing reports whether this session has a channel user sink
	// (telegram/discord/cli/web/feishu/wecom). Required for to=user.
	IsUserFacing() bool
	AgentExists(name string) bool
	SessionExists(key string) bool
	SendToCaller(ctx context.Context, body string) error
	SendToUser(ctx context.Context, body string) error
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
				"- caller:user — reply to whoever woke THIS turn AND assert the caller is the channel user (user-channel wake: telegram/discord/cli/web/feishu/wecom). Fails validation if the actual caller is another session or a system source.\n" +
				"- caller:session — reply to the caller AND assert the caller is another session (cross-session wake; `caller_session_key` is present in wake YAML). Fails validation if the actual caller is the channel user or system.\n" +
				"- user: reply to the channel user via this session's user-channel sink. Only valid for user-facing sessions. Use this when a non-user source (cron/heartbeat/another session) woke you and you want to proactively message YOUR user INSTEAD OF replying to the waker.\n" +
				"- subagent: spawn a new subagent thread, or wake existing at same task_id. Fields: agent (optional), task_id, body.\n" +
				"- fork: branch current session as new agent thread, or wake existing at same task_id. Fields: agent (optional), task_id, body.\n" +
				"- session: wake an existing session. Fields: session_key, body. The target receives the body and its own dispatch(to=caller:session) routes back to YOUR session (ping-pong recurses until one side halts).\n\n" +
				"Which caller form to pick: read `caller_session_key` in the wake YAML frontmatter. Present → to=caller:session; absent AND this session is user-facing → to=caller:user; system sources (cron/heartbeat/compression) have no usable caller form, use dispatch({}) or to=user instead. " +
				"Empty sends — dispatch({}) — is silent turn termination; nothing delivered, history recorded. Only use when you genuinely have nothing to say AND the caller does not need to know you finished. If you received a cross-session wake you believe was mis-routed, dispatch(to=caller:session) with an explanation — do NOT silently drop it via dispatch({}) (the caller never learns). " +
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
									"enum":        []string{"caller:user", "caller:session", "user", "subagent", "fork", "session"},
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
	To          DispatchTarget `json:"to"`
	SessionKey  string         `json:"session_key,omitempty"`
	DeliveredTo string         `json:"delivered_to,omitempty"` // Human-readable destination label. Set for to=caller:* to clarify who received the reply.
	Note        string         `json:"note,omitempty"`
	Preview     string         `json:"preview,omitempty"` // Single-line body preview (≤previewMaxRunes runes) for result readability.
}

const previewMaxRunes = 100

// bodyPreview returns a single-line preview of body, at most previewMaxRunes
// runes, with "..." appended if truncated. Newlines are collapsed to spaces.
func bodyPreview(body string) string {
	s := strings.TrimSpace(body)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	runes := []rune(s)
	if len(runes) <= previewMaxRunes {
		return s
	}
	return string(runes[:previewMaxRunes]) + "..."
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
			"outcome": "turn-terminated-silent",
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
		item.Preview = bodyPreview(send.Body)
		executed = append(executed, item)
	}

	t.host.SignalHalt()
	isUserFacing := t.host.IsUserFacing()
	callerKind, _, _ := t.host.CallerInfo()
	if len(execErrs) > 0 {
		return buildDispatchMixedResult(executed, execErrs, isUserFacing, callerKind)
	}
	return buildDispatchSuccessResult(executed, isUserFacing, callerKind)
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
	case TargetCallerUser:
		if send.Agent != "" || send.TaskID != "" || send.SessionKey != "" {
			return "caller:user does not accept agent/task_id/session_key"
		}
		kind, callerKey, _ := t.host.CallerInfo()
		switch kind {
		case msg.CallerKindUser:
			// OK
		case msg.CallerKindSession:
			return fmt.Sprintf("to=caller:user but actual caller is another session (%s). Use to=caller:session, or to=user to reach your channel user directly.", callerKey)
		case msg.CallerKindSystem:
			return "to=caller:user but actual caller is system (cron/heartbeat/compression — replies are dropped). Use dispatch({}) to end silently, or to=user if you need to reach your channel user."
		default:
			return "current wake has no routable caller"
		}
	case TargetCallerSession:
		if send.Agent != "" || send.TaskID != "" || send.SessionKey != "" {
			return "caller:session does not accept agent/task_id/session_key"
		}
		kind, _, _ := t.host.CallerInfo()
		switch kind {
		case msg.CallerKindSession:
			// OK
		case msg.CallerKindUser:
			return "to=caller:session but actual caller is the channel user. Use to=caller:user, or to=user for direct channel delivery."
		case msg.CallerKindSystem:
			return "to=caller:session but actual caller is system (cron/heartbeat/compression — replies are dropped). Use dispatch({}) to end silently, or to=user."
		default:
			return "current wake has no routable caller"
		}
	case TargetUser:
		if send.Agent != "" || send.TaskID != "" || send.SessionKey != "" {
			return "user does not accept agent/task_id/session_key"
		}
		if !t.host.IsUserFacing() {
			return "current session is not user-facing — to=user is only valid for telegram/discord/cli/web/feishu/wecom sessions"
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
		return fmt.Sprintf("unknown to: %q (must be one of caller:user/caller:session/user/subagent/fork/session)", send.To)
	}
	return ""
}

// targetKey returns a stable string identifying the resolved target, for batch dedup.
func targetKey(send DispatchSend, currentSession string) string {
	switch send.To {
	case TargetCallerUser, TargetCallerSession:
		return "caller" // at most one caller per batch regardless of declared kind
	case TargetUser:
		return "user" // at most one user per batch
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
	case TargetCallerUser, TargetCallerSession:
		_, callerKey, sinkLabel := t.host.CallerInfo()
		if err := t.host.SendToCaller(ctx, send.Body); err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{
			To:          send.To,
			SessionKey:  callerKey,
			DeliveredTo: sinkLabel,
		}, nil
	case TargetUser:
		if err := t.host.SendToUser(ctx, send.Body); err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetUser, SessionKey: t.host.CurrentSessionKey()}, nil
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

// describeExecuted renders one executed dispatch entry as a single line,
// inlining the body preview so the content-to-target mapping is unambiguous:
// the quoted string IS the body that went to this specific target, and nothing
// else. Each entry in the result list stands alone.
func describeExecuted(ex ExecutedItem) string {
	body := `"` + ex.Preview + `"`
	switch ex.To {
	case TargetCallerUser:
		if ex.DeliveredTo != "" {
			return "Replied " + body + " to the caller, the channel user (resolved to: " + ex.DeliveredTo + ")."
		}
		return "Replied " + body + " to the caller (channel user)."
	case TargetCallerSession:
		if ex.DeliveredTo != "" {
			return "Replied " + body + " to the caller session " + ex.SessionKey + " (resolved to: " + ex.DeliveredTo + ")."
		}
		return "Replied " + body + " to the caller session " + ex.SessionKey + "."
	case TargetUser:
		return "Sent " + body + " to your channel user (nothing else was sent to the user)."
	case TargetSubagent:
		note := ex.Note
		if note == "" {
			note = "dispatched"
		}
		return "Spawned subagent at session " + ex.SessionKey + " (" + note + ") with body " + body + "."
	case TargetFork:
		note := ex.Note
		if note == "" {
			note = "dispatched"
		}
		return "Created fork at session " + ex.SessionKey + " (" + note + ") with body " + body + "."
	case TargetSession:
		return "Woke session " + ex.SessionKey + " with body " + body + "."
	}
	return "Dispatched " + body + " to=" + string(ex.To) + " at session " + ex.SessionKey + "."
}

// hasReachedUser reports whether any executed send delivered directly to the
// channel user this turn. True for to=user and to=caller:user (the latter
// asserts the caller IS the channel user). Used to suppress the
// noUserReminder when the reminder would be misleading.
func hasReachedUser(executed []ExecutedItem) bool {
	for _, ex := range executed {
		if ex.To == TargetUser || ex.To == TargetCallerUser {
			return true
		}
	}
	return false
}

const noUserReminder = "Reminder: this dispatch had no to=user entry. Any reply above went to another AI session, not to your channel user. Unless you explicitly dispatch(to=user), nothing in this turn is visible to the human user."

const callerUserRedundantHint = "Hint: this turn was woken by the channel user (caller:user). For a user-facing reply, prefer ending the turn with a plain assistant message — its content is auto-delivered to the channel user, so an explicit dispatch to user/caller:user is redundant and may double-deliver if you also produced text earlier in the turn."

func buildDispatchErrorResult(errs []DispatchError) string {
	var sb strings.Builder
	sb.WriteString("Validation failed — no sends were executed. Fix and re-call dispatch; the turn continues.\n\nErrors:\n")
	for _, e := range errs {
		if e.To != "" {
			fmt.Fprintf(&sb, "  - send #%d (to=%s): %s\n", e.Index, e.To, e.Detail)
		} else {
			fmt.Fprintf(&sb, "  - send #%d: %s\n", e.Index, e.Detail)
		}
	}
	return toolResult("dispatch", map[string]any{
		"outcome": "validation-error",
	}, strings.TrimRight(sb.String(), "\n"))
}

func buildDispatchSuccessResult(executed []ExecutedItem, isUserFacing bool, callerKind msg.CallerKind) string {
	var sb strings.Builder
	if len(executed) == 1 {
		sb.WriteString("Executed 1 send. Turn ended.\n\n")
	} else {
		fmt.Fprintf(&sb, "Executed %d sends. Turn ended.\n\n", len(executed))
	}
	for i, ex := range executed {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, describeExecuted(ex))
	}
	if reached := hasReachedUser(executed); reached && callerKind == msg.CallerKindUser {
		sb.WriteString("\n")
		sb.WriteString(callerUserRedundantHint)
	} else if isUserFacing && !reached {
		sb.WriteString("\n")
		sb.WriteString(noUserReminder)
	}
	return toolResult("dispatch", map[string]any{
		"outcome": "turn-terminated",
	}, strings.TrimRight(sb.String(), "\n"))
}

func buildDispatchMixedResult(executed []ExecutedItem, errs []DispatchError, isUserFacing bool, callerKind msg.CallerKind) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Partial failure: %d send(s) executed, %d failed. Turn ended — executed deliveries cannot be unrolled.\n", len(executed), len(errs))
	if len(executed) > 0 {
		sb.WriteString("\nExecuted:\n")
		for i, ex := range executed {
			fmt.Fprintf(&sb, "  %d. %s\n", i+1, describeExecuted(ex))
		}
	}
	if len(errs) > 0 {
		sb.WriteString("\nFailed:\n")
		for _, e := range errs {
			if e.To != "" {
				fmt.Fprintf(&sb, "  - send #%d (to=%s): %s\n", e.Index, e.To, e.Detail)
			} else {
				fmt.Fprintf(&sb, "  - send #%d: %s\n", e.Index, e.Detail)
			}
		}
	}
	if reached := hasReachedUser(executed); reached && callerKind == msg.CallerKindUser {
		sb.WriteString("\n")
		sb.WriteString(callerUserRedundantHint)
	} else if isUserFacing && !reached {
		sb.WriteString("\n")
		sb.WriteString(noUserReminder)
	}
	return toolResult("dispatch", map[string]any{
		"outcome": "partial-failure",
	}, strings.TrimRight(sb.String(), "\n"))
}
