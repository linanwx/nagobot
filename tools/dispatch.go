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
	// TargetCallerSession replies to the caller AND asserts the caller is
	// another session. Fails validation if the caller is the channel user or
	// a system source. (There is no caller:user form, and no to=user target:
	// speaking to your own human is done by writing content, not by
	// dispatching — see Thread.contentSink.)
	TargetCallerSession DispatchTarget = "caller:session"
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
	TargetCallerSession: {},
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
	// ContentReachesSomeone reports whether text the model writes as assistant
	// content alongside this tool_call actually gets delivered. True on a
	// user-facing session whose wake source routes content to its human (the
	// channel user, a cron injection, a peer-session wake). False when the
	// content would be dropped: heartbeat turns, suppressed sinks, and sessions
	// with no human of their own (subagents, forks) whose sink is not chunkable.
	ContentReachesSomeone() bool
	AgentExists(name string) bool
	SessionExists(key string) bool
	SendToCaller(ctx context.Context, body string) error
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
			Description: "Routing primitive for reaching OTHER agents and sessions. It does NOT reach your own human: to speak to the human on this session's channel, simply write your reply as ordinary assistant text and end the turn — there is no to=user target. The server decides whether that text reaches the human from the wake source alone: a heartbeat or compression turn never reaches the human no matter what it writes, while a user, cron, or peer-session turn does. " +
				"dispatch ends the turn ONLY when it is the sole tool call in your message. Batched alongside other tool calls, every send still delivers but the turn CONTINUES — you will see the other tools' results and keep working.\n" +
				"Each entry in `sends` has a `to` field selecting the target:\n" +
				"- caller:session — reply to the caller AND assert the caller is another session (cross-session wake; `caller_session_key` is present in wake YAML). Fails validation if the actual caller is the channel user or system.\n" +
				"- subagent: spawn a new subagent thread, or wake existing at same task_id. Takes to/body plus params: task_id (required), agent?, provider?+model? (optional model override).\n" +
				"- fork: branch current session as new agent thread, or wake existing at same task_id. Takes to/body plus the same params as subagent.\n" +
				"- session: wake ANOTHER session's AI. The body becomes that session's wake message, processed by ITS AI (own agent/persona/history) — it is NOT delivered verbatim to that session's human user; the target AI decides what, if anything, to say to its own human (by writing its reply text). Takes to/body plus params in one of two mutually exclusive forms: (1) session_key — exact key of an EXISTING session (validation fails if it does not exist); (2) channel + user_id — address a channel endpoint directly, creating the session if missing. Use form 2 to initiate contact with a user who may never have talked to the bot. Either way, the target's dispatch(to=caller:session) routes back to YOUR session (ping-pong recurses until one side halts).\n\n" +
				"Which form to pick when replying to whoever woke you: read `caller_session_key` in the wake YAML frontmatter. Present → dispatch(to=caller:session) (a peer session woke you). Absent AND this session is user-facing → the channel user woke you: do NOT dispatch, just write your reply as ordinary text. System sources (cron/heartbeat/compression) have no caller to reply to — write your reply (delivered only if the source allows) or dispatch({}). " +
				"Empty sends — dispatch({}) — is silent turn termination; nothing delivered, history recorded. Only use when you genuinely have nothing to say AND the caller does not need to know you finished. If you received a cross-session wake you believe was mis-routed, dispatch(to=caller:session) with an explanation — do NOT silently drop it via dispatch({}) (the caller never learns). " +
				"Assistant content alongside a dispatch call: dispatch itself delivers ONLY each send's `body`. The content field is your speech to your own human, and it is delivered independently whenever this turn's wake source allows it — so on a user-facing turn, reporting to your human in content while routing work with dispatch is the normal shape, and when you hand work off you SHOULD tell your human what you just did. Where content reaches nobody (a heartbeat turn, or a subagent/fork with no human of its own) it is rejected with a validation error so the text is not lost — move it into a send body. " +
				"Common mistakes to avoid: (a) do NOT use to=session to reply to whoever woke you — that is to=caller:session; to=session wakes a DIFFERENT session. (b) Do NOT dispatch in order to reach your own human — there is no to=user; end the turn with plain text instead. (c) Do NOT use plain text to answer a cross-session caller — text goes to your own human, not the caller; use to=caller:session. " +
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
									"enum":        []string{"caller:session", "user", "subagent", "fork", "session"},
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

func (t *DispatchTool) run(ctx context.Context, args json.RawMessage) (result string) {
	var a dispatchArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}
	if t.host == nil {
		return toolError("dispatch", "host not configured")
	}
	normalizeSends(a.Sends)

	// Short assistant content alongside dispatch is tolerated but dropped; the
	// warning built in the content check below is appended here to whatever
	// result this call ultimately produces (silent termination, success, mixed,
	// or a later validation error). It is only ever set on the < 50-rune path,
	// so the long-content hard-reject return sees it empty and is unaffected.
	var droppedContentWarning string
	defer func() {
		if droppedContentWarning != "" {
			result += droppedContentWarning
		}
	}()

	// The content+dispatch combo. Where content reaches someone — a user-facing
	// session speaking to its own human — writing text alongside dispatch is the
	// normal shape: the content is the report to the human, the sends are the
	// routing to other agents. Nothing to check.
	//
	// Where it does NOT reach anyone (heartbeat turns, subagents/forks whose sink
	// is not chunkable), that text has no recipient — dispatch delivers only the
	// explicit `body` of each send. Substantial content (>= 50 runes) is a real
	// message the model meant to send, so hard-reject and make it pick a send.
	// Short content (< 50 runes) is usually a stray fragment (an ack, an emoji);
	// rejecting it would force a full turn re-run for no benefit, so it is
	// dropped with a warning (appended via the defer above) and dispatch proceeds.
	content := strings.TrimSpace(provider.AssistantContentFromContext(ctx))
	if t.host.ContentReachesSomeone() {
		content = ""
	}
	if content != "" {
		preview := content
		if runes := []rune(preview); len(runes) > previewMaxRunes {
			preview = string(runes[:previewMaxRunes]) + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		if len([]rune(content)) >= 50 {
			return toolResult("dispatch", map[string]any{
				"outcome": "validation-error",
			}, "Validation failed — no sends were executed. Fix and re-call dispatch; the turn continues.\n\n"+
				"Reason: this turn produced non-empty assistant content alongside the dispatch call, and this session has nobody to deliver it to — there is no human reading this session's replies right now. dispatch only delivers each send's `body` field; the assistant message text is dropped.\n\n"+
				"Offending content (preview): \""+preview+"\"\n\n"+
				"Fix: move ALL of that text into the appropriate send body (e.g. dispatch(sends=[{to: \"caller:session\", body: \"<your text>\"}])), so it reaches whoever asked. If it was meant for nobody, drop it and call dispatch with the assistant message empty.\n"+
				"Re-issue the turn with the text moved into the send body.")
		}
		droppedContentWarning = fmt.Sprintf(
			"\n\n⚠️ Warning: assistant content was DISCARDED — dispatch delivers only each send's `body`, never the assistant message text. Discarded content: %q. If it was meant for the recipient, move it into a send body.",
			preview)
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

	// Validate entire batch first (all-or-nothing on validation): a batch that
	// half-delivers and then reports an error is worse than one that delivers
	// nothing, because executed sends cannot be unrolled.
	if errs := t.validateAll(a.Sends); len(errs) > 0 {
		return buildDispatchErrorResult(errs)
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
	if len(execErrs) > 0 {
		return buildDispatchMixedResult(executed, execErrs, solo)
	}
	return buildDispatchSuccessResult(executed, solo)
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
		return fmt.Sprintf("unknown to: %q (must be one of caller:session/subagent/fork/session). To speak to your own human, do not dispatch at all — just write your message as your reply text.", send.To)
	}
	if detail := rejectBadParams(send, accepted); detail != "" {
		return detail
	}

	// Per-target semantic validation. Everything below assumes the send carries
	// only fields its target accepts.
	switch send.To {
	case TargetCallerSession:
		kind, _, _ := t.host.CallerInfo()
		switch kind {
		case msg.CallerKindSession:
			// OK
		case msg.CallerKindUser:
			return "to=caller:session but actual caller is the channel user. To answer your own human, just write your reply as content — no dispatch needed."
		case msg.CallerKindSystem:
			return "to=caller:session but actual caller is system (cron/heartbeat/compression — replies are dropped). This wake has no session caller; choose an explicit target instead."
		default:
			return "current wake has no routable caller"
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
				return "session_key is the current session (self-reference not allowed). To speak to THIS session's own human, do not dispatch at all — just write your message as your reply text."
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
				return "channel+user_id resolves to the current session (self-reference not allowed). To speak to THIS session's own human, do not dispatch at all — just write your message as your reply text."
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
	case TargetCallerSession:
		return "caller" // at most one caller per batch regardless of declared kind
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
	case TargetCallerSession:
		_, callerKey, sinkLabel := t.host.CallerInfo()
		if err := t.host.SendToCaller(ctx, send.Body); err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{
			To:          send.To,
			SessionKey:  callerKey,
			DeliveredTo: sinkLabel,
		}, nil
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
	case TargetCallerSession:
		if ex.DeliveredTo != "" {
			return "Replied " + body + " to the caller session " + ex.SessionKey + " (resolved to: " + ex.DeliveredTo + ")."
		}
		return "Replied " + body + " to the caller session " + ex.SessionKey + "."
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

func buildDispatchSuccessResult(executed []ExecutedItem, solo bool) string {
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
	outcome := "turn-terminated"
	if !solo {
		outcome = "delivered-turn-continues"
	}
	return toolResult("dispatch", map[string]any{
		"outcome": outcome,
	}, strings.TrimRight(sb.String(), "\n"))
}

func buildDispatchMixedResult(executed []ExecutedItem, errs []DispatchError, solo bool) string {
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
	return toolResult("dispatch", map[string]any{
		"outcome": "partial-failure",
	}, strings.TrimRight(sb.String(), "\n"))
}
