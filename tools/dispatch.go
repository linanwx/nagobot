package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
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

// DispatchSend is a single dispatch entry. Params is a free-form string
// dictionary — deliberately NOT a struct and NOT enumerated in the tool
// schema. Models trained on strict structured outputs emit every declared
// property of every object (blanking the unused ones with ""), and with seven
// declared addressing fields one of them eventually gets a plausible wrong
// value instead of a blank (observed live: `channel:"discord"` pinned onto
// to=user for 15 identical rejected calls while a medication reminder was
// silently dropped). A dictionary declares nothing, so there is nothing to
// compulsively fill: the simple targets send `params: {}` and the others
// write exactly the keys they need. Key validation happens here, per target,
// with guidance in every rejection.
type DispatchSend struct {
	To     DispatchTarget    `json:"to"`
	Body   string            `json:"body"`
	Params map[string]string `json:"params,omitempty"`
}

// endpointChannels lists the channels addressable via the to=session
// channel+user_id endpoint form. Sorted — the list feeds the tool schema enum
// and prompt caching requires deterministic serialization.
var endpointChannels = []string{"discord", "feishu", "telegram", "wecom"}

func isEndpointChannel(name string) bool {
	return slices.Contains(endpointChannels, name)
}

// dispatchParamKeys is every params key any target understands, in the order
// errors report them. A key outside this list is rejected by name.
var dispatchParamKeys = []string{"agent", "task_id", "provider", "model", "session_key", "channel", "user_id"}

// acceptedFields is the per-target whitelist of params keys. It is a
// WHITELIST, not a per-branch reject list, and that is the whole point: a key
// understood by one target is rejected on every other until that target opts
// in. The pre-params layout once accepted-then-ignored channel/user_id on four
// of the six targets — the model got no error, so it read silence as
// acceptance while its intended delivery never happened.
var acceptedFields = map[DispatchTarget]map[string]bool{
	TargetCallerUser:    {},
	TargetCallerSession: {},
	TargetUser:          {},
	TargetSubagent:      {"agent": true, "task_id": true, "provider": true, "model": true},
	TargetFork:          {"agent": true, "task_id": true, "provider": true, "model": true},
	TargetSession:       {"session_key": true, "channel": true, "user_id": true},
}

// dispatchFieldHints explain what a misplaced params key is actually for, so
// the rejection tells the model where the key belongs rather than only that it
// does not belong here.
var dispatchFieldHints = map[string]string{
	"agent":       "agent/task_id belong to to=subagent/fork",
	"task_id":     "agent/task_id belong to to=subagent/fork",
	"provider":    "the provider+model override applies to to=subagent/fork only",
	"model":       "the provider+model override applies to to=subagent/fork only",
	"session_key": "session_key addresses an existing session via to=session",
	"channel":     "channel+user_id address a channel endpoint via to=session",
	"user_id":     "channel+user_id address a channel endpoint via to=session",
}

// normalizeSends trims every send's to and params keys/values, once, at the
// entry point, and deletes params entries whose trimmed value is empty — an
// empty value is "not provided" (models that cannot omit keys blank them), and
// deleting it up front means presence below is a plain non-empty map lookup.
// Body is left untouched — leading and trailing whitespace is part of the
// payload.
//
// Trimming here means presence checks, equality guards, existence lookups and
// execution all see the same value. They used to disagree: presence was tested
// with a bare != "" in some places and strings.TrimSpace(...) != "" in others,
// giving a whitespace-only value two identities at once. The sharp edge was
// session_key: the hasKey gate trimmed, the self-reference guard did not, and
// execution trimmed again — so `session_key: "cli:main "` slipped past the
// self-wake guard and then failed its existence check on a session that plainly
// exists. Today only that existence check stands between a trailing space and
// self-wake recursion.
func normalizeSends(sends []DispatchSend) {
	for i := range sends {
		s := &sends[i]
		s.To = DispatchTarget(strings.TrimSpace(string(s.To)))
		if len(s.Params) == 0 {
			continue
		}
		clean := make(map[string]string, len(s.Params))
		for k, v := range s.Params {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k == "" || v == "" {
				continue
			}
			clean[k] = v
		}
		s.Params = clean
	}
}

// rejectBadParams returns a validation detail if the send's params carry a key
// no target understands, or a key its own target does not accept; "" when the
// send is clean. The rejection is guidance, not just a verdict: it names where
// each misplaced key belongs, and for the to/body-only targets it includes the
// exact corrected JSON to resend — a model that pinned a plausible-but-wrong
// key needs a copy-paste replacement, not prose.
func rejectBadParams(send DispatchSend, accepted map[string]bool) string {
	var unknown, bad, hints []string
	for _, k := range slices.Sorted(maps.Keys(send.Params)) {
		if !slices.Contains(dispatchParamKeys, k) {
			unknown = append(unknown, k)
			continue
		}
		if accepted[k] {
			continue
		}
		bad = append(bad, k)
		if h := dispatchFieldHints[k]; h != "" && !slices.Contains(hints, h) {
			hints = append(hints, h)
		}
	}
	if len(unknown) > 0 {
		return fmt.Sprintf("unknown params key(s): %s (valid keys: %s)",
			strings.Join(unknown, "/"), strings.Join(dispatchParamKeys, "/"))
	}
	if len(bad) == 0 {
		return ""
	}
	accepts := "no params at all"
	if len(accepted) > 0 {
		names := slices.Sorted(maps.Keys(accepted))
		accepts = strings.Join(names, "/")
	}
	detail := fmt.Sprintf("%s does not accept params %s (accepts: %s). %s",
		send.To, strings.Join(bad, "/"), accepts, strings.Join(hints, "; "))
	if len(accepted) == 0 {
		detail += fmt.Sprintf(". %s already knows its destination — to deliver this message, resend exactly: %s",
			send.To, correctedSendJSON(send.To, send.Body))
	}
	return detail
}

// correctedSendJSON renders the exact JSON to resend for a to/body-only
// target — copy-paste self-healing beats prose when a model is repeating a
// rejected shape. Long bodies are elided so a validation error cannot double
// a huge payload in context.
func correctedSendJSON(to DispatchTarget, body string) string {
	if runes := []rune(body); len(runes) > 300 {
		body = "<same body unchanged>"
	}
	entry, _ := json.Marshal(map[string]string{"to": string(to), "body": body})
	return `{"sends":[` + string(entry) + `]}`
}

// resolvedSessionKey returns the target session key of a to=session send:
// the explicit session_key, or channel+":"+user_id for the endpoint form.
// Empty when neither form is complete. Params are already trimmed and
// empty-pruned by normalizeSends.
func resolvedSessionKey(send DispatchSend) string {
	if k := send.Params["session_key"]; k != "" {
		return k
	}
	ch, uid := send.Params["channel"], send.Params["user_id"]
	if ch == "" || uid == "" {
		return ""
	}
	return ch + ":" + uid
}

// DispatchHost abstracts the thread-side operations dispatch needs.
type DispatchHost interface {
	CurrentSessionKey() string
	// CallerInfo returns an atomic snapshot of the current wake's caller:
	// kind — "user" when the caller is the channel user; "session" when the
	//        caller is another session (cross-session wake); "system" when
	//        the caller is cron/heartbeat/compression/resume (drop
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
	CreateOrWakeSubagent(ctx context.Context, agent, taskID, body, overrideProvider, overrideModel string) (sessionKey, note string, err error)
	CreateOrWakeFork(ctx context.Context, agent, taskID, body, overrideProvider, overrideModel string) (sessionKey, note string, err error)
	// ValidateModelOverride reports whether (provider, model) is a usable model
	// override for a subagent/fork wake: the provider must have a configured API
	// key and the model must be in that provider's whitelist. Returns a
	// descriptive error otherwise (never silent). Both args are non-empty.
	ValidateModelOverride(provider, model string) error
	WakeSession(ctx context.Context, sessionKey, body string) error
	SignalHalt()
	// ClearSuppressSink re-enables end-of-turn sink delivery. Called after a
	// batched (non-terminating) dispatch: SendToCaller suppresses the sink to
	// prevent double delivery, but a continuing turn must still deliver its
	// eventual final text.
	ClearSuppressSink()
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
				"dispatch ends the turn ONLY when it is the sole tool call in your message. Batched alongside other tool calls, every send still delivers but the turn CONTINUES — you will see the other tools' results and keep working. Use that batched form deliberately to give the user a brief progress note while kicking off longer work (e.g. dispatch(to=caller:user, \"Searching...\") + web_search in one message), then end the turn later with a final dispatch called alone or with plain text.\n" +
				"Each entry in `sends` has a `to` field selecting the target:\n" +
				"- caller:user — reply to whoever woke THIS turn AND assert the caller is the channel user (user-channel wake: telegram/discord/cli/web/feishu/wecom). Fails validation if the actual caller is another session or a system source.\n" +
				"- caller:session — reply to the caller AND assert the caller is another session (cross-session wake; `caller_session_key` is present in wake YAML). Fails validation if the actual caller is the channel user or system.\n" +
				"- user: message YOUR user — the human on this session's own channel. Only valid for user-facing sessions. Use this when a non-user source (cron/heartbeat/another session) woke you and you want to proactively message your user INSTEAD OF replying to the waker. Takes only to/body: the destination is this session itself, so params stays empty.\n" +
				"- subagent: spawn a new subagent thread, or wake existing at same task_id. Takes to/body plus params: task_id (required), agent?, provider?+model? (optional model override).\n" +
				"- fork: branch current session as new agent thread, or wake existing at same task_id. Takes to/body plus the same params as subagent.\n" +
				"- session: wake ANOTHER session's AI. The body becomes that session's wake message, processed by ITS AI (own agent/persona/history) — it is NOT delivered verbatim to that session's human user; the target AI decides what, if anything, to forward (typically via its own dispatch(to=user)). Takes to/body plus params in one of two mutually exclusive forms: (1) session_key — exact key of an EXISTING session (validation fails if it does not exist); (2) channel + user_id — address a channel endpoint directly, creating the session if missing. Use form 2 to initiate contact with a user who may never have talked to the bot. Either way, the target's dispatch(to=caller:session) routes back to YOUR session (ping-pong recurses until one side halts).\n\n" +
				"Which caller form to pick: read `caller_session_key` in the wake YAML frontmatter. Present → to=caller:session; absent AND this session is user-facing → to=caller:user; system sources (cron/heartbeat/compression) have no usable caller form, use dispatch({}) or to=user instead. " +
				"Empty sends — dispatch({}) — is silent turn termination; nothing delivered, history recorded. Only use when you genuinely have nothing to say AND the caller does not need to know you finished. If you received a cross-session wake you believe was mis-routed, dispatch(to=caller:session) with an explanation — do NOT silently drop it via dispatch({}) (the caller never learns). " +
				"IMPORTANT: when calling dispatch, the assistant message's content field MUST be empty. dispatch only delivers each send's `body`; any text written in content alongside this tool_call has no defined recipient and will be rejected. Either put all user-facing text into a send body, or skip dispatch entirely and let default delivery route your assistant content to the caller. " +
				"Common mistakes to avoid: (a) do NOT use to=session to reply to whoever woke you — that is to=caller:session; to=session wakes a DIFFERENT session. (b) Do NOT use to=user to answer a cross-session caller — that messages your own human, not the caller; use to=caller:session. (c) On a user-facing session, a dispatch with none of to=user / to=caller:user means the human sees NOTHING this turn, even if subagents ran. " +
				"On success the turn ends (if dispatch was the sole tool call). On validation error the turn continues — fix and re-call. " +
				"dispatch fires NOW — it has no delay/schedule parameter. For any future or delayed wake (including a delayed self-wake), use the manage-cron skill, not dispatch.",
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
									"description": "Message body delivered to the target. For to=session it is a wake instruction processed by the target's AI — write it as a directive to that AI, NOT as verbatim text for that session's human.",
								},
								"params": map[string]any{
									"type": "object",
									"description": "Target-specific options dictionary (string values). Write ONLY the keys your target needs; leave the whole dictionary empty for user and caller:*, which already know their destination. " +
										"For to=subagent/fork — task_id: required, [a-z0-9_-]+, reusing the same task_id targets the existing thread; agent: template name, empty for the session default (never invent placeholders like \"default\"); provider+model: optional model override, must be set together, list valid pairs via `set-model --list-fallback`. " +
										"For to=session — EITHER session_key: exact key of an existing session, OR channel+user_id: channel one of discord/feishu/telegram/wecom, user_id the channel-native recipient id (wecom userid, telegram chat id, discord channel id, feishu openID; groups per channel convention, e.g. wecom \"group:<chatid>\"), session created if missing.",
									"additionalProperties": map[string]any{"type": "string"},
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

// BodyPreview returns a single-line preview of body, at most previewMaxRunes
// runes, with "..." appended if truncated. Newlines are collapsed to spaces.
// Exported so other packages (e.g. thread post-hook breadcrumbs) can produce
// preview strings consistent with dispatch's tool-result formatting.
func BodyPreview(body string) string {
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
	normalizeSends(a.Sends)

	// Reject the content+dispatch combo. When the model emits text in the
	// assistant content field alongside this dispatch tool_call, that text has
	// no defined recipient: dispatch only delivers the explicit `body` of each
	// send. Allowing it leaks content inconsistently across channels (chunkable
	// sinks forward it as intermediate, non-chunkable drop it) and lets the
	// model assume delivery that does not happen. Force the model to either
	// move all user-facing text into a send body, or end the turn without
	// dispatch and let the default sink delivery handle pure content.
	if content := strings.TrimSpace(provider.AssistantContentFromContext(ctx)); content != "" {
		preview := content
		if runes := []rune(preview); len(runes) > previewMaxRunes {
			preview = string(runes[:previewMaxRunes]) + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		return toolResult("dispatch", map[string]any{
			"outcome": "validation-error",
		}, "Validation failed — no sends were executed. Fix and re-call dispatch; the turn continues.\n\n"+
			"Reason: this turn produced non-empty assistant content alongside the dispatch call. dispatch only delivers each send's `body` field — content emitted in the assistant message itself has no defined recipient and will not be delivered as you might expect.\n\n"+
			"Offending content (preview): \""+preview+"\"\n\n"+
			"Fix: move ALL user-facing text into the appropriate send body (e.g. dispatch(sends=[{to: \"caller:user\", body: \"<your text>\"}])). When you call dispatch, the assistant message must carry no user-facing text — every word you want delivered goes in a send's `body`.\n"+
			"Re-issue the turn with the text moved into the send body.")
	}

	// Solo = dispatch is the only tool call in this assistant message.
	// Batched with other tool calls, sends still deliver but the turn
	// continues: halting here would discard the sibling tools' results and
	// cut off the model's reasoning mid-work. Batch size 0 means the caller
	// didn't plumb the context (tests, direct invocation) — treat as solo.
	solo := provider.ToolBatchSizeFromContext(ctx) <= 1

	// HIGHEST PRIORITY: dispatch({}) is ALWAYS a valid silent termination,
	// regardless of session kind, caller kind, or any other rule. The model
	// explicitly chose to say nothing — respect it and end the turn.
	if len(a.Sends) == 0 {
		if !solo {
			return toolResult("dispatch", map[string]any{
				"outcome": "no-op",
			}, "Nothing sent and the turn was NOT terminated: dispatch only ends the turn when it is the sole tool call in your message, and this call was batched with other tool calls. Continue working with their results; when finished, call dispatch({}) alone to end the turn silently.")
		}
		t.host.SignalHalt()
		return toolResult("dispatch", map[string]any{
			"outcome": "turn-terminated-silent",
		}, "Turn terminated silently. No delivery; history recorded.")
	}

	// Validate entire batch first (all-or-nothing on validation). Per-send
	// validation runs before the user-progress rule so that specific errors
	// (unknown to=, malformed task_id, missing session_key, caller-kind
	// mismatch, etc.) get specific feedback instead of being shadowed by a
	// generic "no user-facing target" message.
	if errs := t.validateAll(a.Sends); len(errs) > 0 {
		return buildDispatchErrorResult(errs)
	}

	// User-progress enforcement: on a user-facing session woken
	// "interactively" — by the channel user OR by another session (peer
	// asking, child reporting back via child_completed) — a non-empty
	// dispatch call MUST include a user-facing target (to=user or
	// to=caller:user). Otherwise the user gets no progress update from this
	// turn even though real work happened (subagents spawned, peers
	// notified, etc.). System wakes (cron / heartbeat / compression /
	// resume) are exempt — silent skip is part of their design.
	//
	// Escape valve: the model can still skip dispatch entirely and let
	// naive assistant text auto-route to the user via the default sink, OR
	// call dispatch({}) to deliberately stay silent (handled above).
	callerKind, _, _ := t.host.CallerInfo()
	enforceUserProgress := t.host.IsUserFacing() &&
		(callerKind == msg.CallerKindUser || callerKind == msg.CallerKindSession)
	if enforceUserProgress && !batchHasUserFacingSend(a.Sends) {
		return buildUserDispatchRequiredResult(a.Sends)
	}

	// Execute. Partial failure possible — the halt decision (solo only) is
	// the same either way: executed deliveries cannot be unrolled.
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
		item.Preview = BodyPreview(send.Body)
		executed = append(executed, item)
	}

	if solo {
		t.host.SignalHalt()
	} else {
		t.host.ClearSuppressSink()
	}
	isUserFacing := t.host.IsUserFacing()
	if len(execErrs) > 0 {
		return buildDispatchMixedResult(executed, execErrs, isUserFacing, solo)
	}
	return buildDispatchSuccessResult(executed, isUserFacing, solo)
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

// validateOne checks a single send. Fields are already trimmed by
// normalizeSends, so presence is a plain != "" everywhere below.
func (t *DispatchTool) validateOne(send DispatchSend, currentSession string) string {
	if strings.TrimSpace(send.Body) == "" {
		return "body is required"
	}
	// Key admission is a whitelist keyed by target; an unknown target has no
	// entry, which makes this lookup the unknown-to check as well.
	accepted, known := acceptedFields[send.To]
	if !known {
		return fmt.Sprintf("unknown to: %q (must be one of caller:user/caller:session/user/subagent/fork/session)", send.To)
	}
	if detail := rejectBadParams(send, accepted); detail != "" {
		return detail
	}

	// Per-target semantic validation. Everything below assumes the send carries
	// only fields its target accepts.
	switch send.To {
	case TargetCallerUser:
		kind, callerKey, _ := t.host.CallerInfo()
		switch kind {
		case msg.CallerKindUser:
			// OK
		case msg.CallerKindSession:
			return fmt.Sprintf("to=caller:user but actual caller is another session (%s). Use to=caller:session, or to=user to reach your channel user directly.", callerKey)
		case msg.CallerKindSystem:
			return "to=caller:user but actual caller is system (cron/heartbeat/compression — replies are dropped). This wake has no channel-user caller; choose an explicit target instead."
		default:
			return "current wake has no routable caller"
		}
	case TargetCallerSession:
		kind, _, _ := t.host.CallerInfo()
		switch kind {
		case msg.CallerKindSession:
			// OK
		case msg.CallerKindUser:
			return "to=caller:session but actual caller is the channel user. Use to=caller:user, or to=user for direct channel delivery."
		case msg.CallerKindSystem:
			return "to=caller:session but actual caller is system (cron/heartbeat/compression — replies are dropped). This wake has no session caller; choose an explicit target instead."
		default:
			return "current wake has no routable caller"
		}
	case TargetUser:
		if !t.host.IsUserFacing() {
			return "current session is not user-facing — to=user is only valid for telegram/discord/cli/web/feishu/wecom sessions"
		}
	case TargetSubagent, TargetFork:
		taskID := send.Params["task_id"]
		if taskID == "" {
			return "params.task_id is required"
		}
		if !taskIDRegex.MatchString(taskID) {
			return "params.task_id must match [a-z0-9_-]+"
		}
		if agent := send.Params["agent"]; agent != "" && !t.host.AgentExists(agent) {
			return fmt.Sprintf("agent %q not found — leave params.agent out to use the session default", agent)
		}
		if p, m := send.Params["provider"], send.Params["model"]; p != "" || m != "" {
			if p == "" || m == "" {
				return "params.provider and params.model must be set together for a model override"
			}
			if err := t.host.ValidateModelOverride(p, m); err != nil {
				return err.Error()
			}
		}
	case TargetSession:
		key := send.Params["session_key"]
		hasKey := key != ""
		hasEndpoint := send.Params["channel"] != "" || send.Params["user_id"] != ""
		switch {
		case hasKey && hasEndpoint:
			return "session accepts either session_key OR channel+user_id, not both"
		case hasKey:
			if key == currentSession {
				return fmt.Sprintf("session_key is the current session (self-reference not allowed). To message THIS session's own user, resend exactly: %s", correctedSendJSON(TargetUser, send.Body))
			}
			if !t.host.SessionExists(key) {
				return fmt.Sprintf("session %q not found — the session_key form requires an existing session. To contact a channel user who may have no session yet, use channel+user_id instead", key)
			}
		case hasEndpoint:
			ch, uid := send.Params["channel"], send.Params["user_id"]
			if ch == "" || uid == "" {
				return "channel and user_id must both be set"
			}
			if !isEndpointChannel(ch) {
				return fmt.Sprintf("unknown channel %q (must be one of %s)", ch, strings.Join(endpointChannels, "/"))
			}
			if strings.ContainsAny(uid, " \t\r\n") {
				return "user_id must not contain whitespace"
			}
			if strings.Contains(uid, ":threads:") || strings.Contains(uid, ":fork:") {
				return "user_id cannot address subagent/fork sessions"
			}
			if ch+":"+uid == currentSession {
				return fmt.Sprintf("channel+user_id resolves to the current session (self-reference not allowed). To message THIS session's own user, resend exactly: %s", correctedSendJSON(TargetUser, send.Body))
			}
		default:
			return "session requires params: either session_key (existing session) or channel+user_id (created if missing)"
		}
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
		return currentSession + ":threads:" + send.Params["task_id"]
	case TargetFork:
		return currentSession + ":fork:" + send.Params["task_id"]
	case TargetSession:
		return resolvedSessionKey(send)
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
		p := send.Params
		key, note, err := t.host.CreateOrWakeSubagent(ctx, p["agent"], p["task_id"], send.Body, p["provider"], p["model"])
		if err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetSubagent, SessionKey: key, Note: note}, nil
	case TargetFork:
		p := send.Params
		key, note, err := t.host.CreateOrWakeFork(ctx, p["agent"], p["task_id"], send.Body, p["provider"], p["model"])
		if err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetFork, SessionKey: key, Note: note}, nil
	case TargetSession:
		key := resolvedSessionKey(send)
		note := ""
		if send.Params["session_key"] == "" && !t.host.SessionExists(key) {
			note = "created"
		}
		if err := t.host.WakeSession(ctx, key, send.Body); err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetSession, SessionKey: key, Note: note}, nil
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
		if ex.Note == "created" {
			return "Created new session " + ex.SessionKey + " and woke it with body " + body + "."
		}
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

// batchHasUserFacingSend reports whether the batch contains at least one send
// that delivers to the channel user (to=user or to=caller:user). Empty batch
// is treated as not user-facing.
func batchHasUserFacingSend(sends []DispatchSend) bool {
	for _, s := range sends {
		if s.To == TargetUser || s.To == TargetCallerUser {
			return true
		}
	}
	return false
}

// buildUserDispatchRequiredResult is the validation-error response when a
// user-facing session (woken interactively by user or another session)
// produced a non-empty dispatch with no user-facing target. The turn
// continues so the model can re-call. Note: dispatch({}) is intentionally
// allowed elsewhere as the silent-termination escape valve and never reaches
// this function.
func buildUserDispatchRequiredResult(sends []DispatchSend) string {
	var sb strings.Builder
	sb.WriteString("Validation failed — no sends were executed. Fix and re-call dispatch; the turn continues.\n\n")
	fmt.Fprintf(&sb, "Reason: this is a user-facing session and the wake came from the user or a peer session, so any non-empty dispatch MUST include a user-facing target (to=user or to=caller:user) so the user sees progress. Your batch had %d send(s) but none targeted the user — work happened (subagent / fork / session / caller:session) but the user got nothing.\n\n", len(sends))
	sb.WriteString("Fix by ONE of:\n")
	sb.WriteString("  1. Add a to=caller:user (or to=user) send to the batch with a brief progress message — e.g. dispatch(sends=[{to: \"caller:user\", body: \"Working on it — will report back.\"}, ...your other sends...]).\n")
	sb.WriteString("  2. Skip dispatch entirely this turn and let your assistant text auto-deliver to the user (works for plain reply-to-user turns).\n")
	sb.WriteString("  3. Genuinely have nothing to say AND nothing to spawn? Use dispatch({}) for silent termination (always allowed, highest priority).\n")
	return toolResult("dispatch", map[string]any{
		"outcome": "validation-error",
	}, strings.TrimRight(sb.String(), "\n"))
}

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

// turnContinuesNote explains a batched dispatch's non-terminating result.
// Deliveries above it in the result are real — the model must not resend.
const turnContinuesNote = "Turn NOT terminated: this dispatch was batched with other tool calls, and dispatch only ends the turn when it is the sole tool call in your message. The deliveries above are already sent — do NOT resend them. Continue working with the other tools' results; then end the turn with a final dispatch called alone, or with plain text (default delivery)."

func buildDispatchSuccessResult(executed []ExecutedItem, isUserFacing, solo bool) string {
	var sb strings.Builder
	ending := "Turn ended."
	if !solo {
		ending = "Turn continues."
	}
	if len(executed) == 1 {
		fmt.Fprintf(&sb, "Executed 1 send. %s\n\n", ending)
	} else {
		fmt.Fprintf(&sb, "Executed %d sends. %s\n\n", len(executed), ending)
	}
	for i, ex := range executed {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, describeExecuted(ex))
	}
	if !solo {
		sb.WriteString("\n")
		sb.WriteString(turnContinuesNote)
	}
	if isUserFacing && !hasReachedUser(executed) {
		sb.WriteString("\n")
		sb.WriteString(noUserReminder)
	}
	outcome := "turn-terminated"
	if !solo {
		outcome = "delivered-turn-continues"
	}
	return toolResult("dispatch", map[string]any{
		"outcome": outcome,
	}, strings.TrimRight(sb.String(), "\n"))
}

func buildDispatchMixedResult(executed []ExecutedItem, errs []DispatchError, isUserFacing, solo bool) string {
	var sb strings.Builder
	ending := "Turn ended"
	if !solo {
		ending = "Turn continues"
	}
	fmt.Fprintf(&sb, "Partial failure: %d send(s) executed, %d failed. %s — executed deliveries cannot be unrolled.\n", len(executed), len(errs), ending)
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
	if !solo {
		sb.WriteString("\n")
		sb.WriteString(turnContinuesNote)
	}
	if isUserFacing && !hasReachedUser(executed) {
		sb.WriteString("\n")
		sb.WriteString(noUserReminder)
	}
	return toolResult("dispatch", map[string]any{
		"outcome": "partial-failure",
	}, strings.TrimRight(sb.String(), "\n"))
}
