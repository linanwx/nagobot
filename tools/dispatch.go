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
	// TargetSubagentFork is named as a variant of TargetSubagent rather than a
	// peer concept, because that is what it is: same spawn, same params, the
	// only difference being that the child inherits stripped history instead of
	// starting fresh. The old name was "fork", which sorted between "session"
	// and "subagent" in the schema enum and read as a fourth unrelated target.
	// Measured before renaming: 8 of 3,525 sends on this deployment, all eight
	// from the developer CLI session, zero from any real channel.
	//
	// The SESSION KEY infix is deliberately NOT renamed with it —
	// session.ForkSessionInfix stays ":fork:". The infix is an internal key
	// shape the model never types, and changing it would make every existing
	// {parent}:fork:{task} key stop matching the child-session tests that keep
	// such a session from being treated as user-facing.
	TargetSubagentFork DispatchTarget = "subagent_fork"
	TargetSession      DispatchTarget = "session"
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
	TargetSubagentFork:  {"agent": true, "task_id": true, "provider": true, "model": true},
	TargetSession:       {"session_key": true, "channel": true, "user_id": true},
}

// targetsAccepting formats the sorted targets whose params whitelist admits
// key, as "to=a/to=b".
//
// DERIVED from acceptedFields for the same reason dispatchTargetNames is, and
// with a sharper edge: these strings are GUIDANCE, not validation. A stale
// enum at least got the model rejected; a stale hint rejects nothing, fails no
// test, and simply points somewhere wrong. Renaming to=fork to to=subagent_fork
// would have left four of them naming a target that no longer exists.
func targetsAccepting(key string) string {
	out := make([]string, 0, len(acceptedFields))
	for target, fields := range acceptedFields {
		if fields[key] {
			out = append(out, "to="+string(target))
		}
	}
	slices.Sort(out)
	return strings.Join(out, "/")
}

// dispatchTargetNames is the sorted list of legal `to` values, DERIVED from
// acceptedFields rather than written out beside it.
//
// It is derived because the hand-written copy was wrong for weeks. When
// to=user was removed, the constants, acceptedFields, the dispatch section,
// thread-ops and dispatch-guide were all updated — and the tool schema's enum
// was not, so it kept telling every model on every turn that "user" was a legal
// target while validateOne rejected it. The schema is the machine-readable
// contract and it sits in the cached tool prefix, so it outweighs any prose
// saying otherwise: a wecom session that had no skill mentioning to=user still
// emitted it, and one weekly review was never delivered because of the
// recovery the rejection provoked. Two copies of one fact with nothing forcing
// them to agree is the defect; deleting the stale copy would only have reset
// the clock.
//
// Sorted because this feeds the tool schema, and prompt caching requires
// map-derived output to serialize deterministically.
func dispatchTargetNames() []string {
	out := make([]string, 0, len(acceptedFields))
	for t := range acceptedFields {
		out = append(out, string(t))
	}
	slices.Sort(out)
	return out
}

// dispatchFieldHints explain what a misplaced params key is actually for, so
// the rejection tells the model where the key belongs rather than only that it
// does not belong here.
var dispatchFieldHints = map[string]string{
	"agent":       "agent/task_id belong to " + targetsAccepting("agent"),
	"task_id":     "agent/task_id belong to " + targetsAccepting("task_id"),
	"provider":    "the provider+model override applies to " + targetsAccepting("provider") + " only",
	"model":       "the provider+model override applies to " + targetsAccepting("model") + " only",
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
	// SignalHalt ends the turn once the current tool batch completes. Only
	// dispatch({}) uses it; every routing dispatch leaves the turn running.
	SignalHalt()
}

// DispatchTool is the asynchronous routing primitive between sessions.
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
			Description: "Asynchronous routing primitive for reaching OTHER agents and sessions. It does NOT reach your own human: to speak to the human on this session's channel, simply write your reply as ordinary assistant text; there is no to=user target. The server decides whether that text reaches the human from the wake source alone: a heartbeat or compression turn never reaches the human no matter what it writes, while a user, cron, or peer-session turn does.\n" +
				"dispatch is asynchronous: every send is delivered and your turn CONTINUES: you get the tool result and keep working. It never waits for the target. When you hand work to a subagent, tell your human what you did in your reply text and finish the turn; do NOT wait or poll. When a subagent/fork you dispatched ends its turn, you are notified automatically by a `progress` wake carrying a short report and the child's session file. Read the file to see its actual result.\n" +
				"Each entry in `sends` has a `to` field selecting the target:\n" +
				"- caller:session: send a message to the session that woke you AND assert the caller is another session (`caller_session_key` is present in wake YAML). Fails validation if the actual caller is the channel user or system.\n" +
				"- subagent: spawn a new subagent thread, or wake the existing one at the same task_id (e.g. to ask it to fix or continue its work). Takes to/body plus params: task_id (required), agent?, provider?+model? (optional model override).\n" +
				"- subagent_fork: same as subagent, but the new thread inherits your (stripped) history. Takes the same params.\n" +
				"- session: wake ANOTHER session's AI. The body becomes that session's wake message, processed by ITS AI (own agent/persona/history). It is NOT delivered verbatim to that session's human user; the target AI decides what, if anything, to say to its own human (by writing its reply text). Takes to/body plus params in one of two mutually exclusive forms: (1) session_key: exact key of an EXISTING session (validation fails if it does not exist); (2) channel + user_id: address a channel endpoint directly, creating the session if missing. Use form 2 to initiate contact with a user who may never have talked to the bot. Either way, the target's dispatch(to=caller:session) routes back to YOUR session.\n\n" +
				"If you are a dispatched subagent/fork yourself: your final reply text IS your result: the thread that dispatched you is notified when your turn ends and reads it from your session. You do not need to dispatch it back; to=caller:session is optional, for sending a message mid-work.\n" +
				"Which form to pick when answering whoever woke you: read `caller_session_key` in the wake YAML frontmatter. Present → dispatch(to=caller:session) (a peer session woke you). Absent AND this session is user-facing → the channel user woke you: do NOT dispatch, just write your reply as ordinary text. System sources (cron/heartbeat/compression) have no caller to reply to: write your reply (delivered only if the source allows) or dispatch({}). " +
				"Empty sends, i.e. dispatch({}), is the one form that ENDS the turn: silent termination, nothing delivered, history recorded, and only when it is the sole tool call in your message (batched with other tool calls it is a no-op). Use it when you genuinely have nothing to say. If you received a cross-session wake you believe was mis-routed, dispatch(to=caller:session) with an explanation; do NOT silently drop it via dispatch({}) (the caller never learns). " +
				"Common mistakes to avoid: (a) do NOT use to=session to reply to whoever woke you, that is to=caller:session; to=session wakes a DIFFERENT session. (b) Do NOT dispatch in order to reach your own human: there is no to=user, write plain text instead. (c) Do NOT use plain text to answer a cross-session peer caller: text goes to your own human, not the caller; use to=caller:session. " +
				"On validation error nothing is sent: fix and re-call. " +
				"dispatch fires NOW — it has no delay/schedule parameter. For any future or delayed wake (including a delayed self-wake), use the manage-cron skill, not dispatch.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sends": map[string]any{
						"type":        "array",
						"description": "List of dispatch entries. Empty or omitted (dispatch({}) called alone) silently ends the turn.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"to": map[string]any{
									"type":        "string",
									"enum":        dispatchTargetNames(),
									"description": "Target kind.",
								},
								"body": map[string]any{
									"type":        "string",
									"description": "Message body delivered to the target. For to=session it is a wake instruction processed by the target's AI — write it as a directive to that AI, NOT as verbatim text for that session's human.",
								},
								"params": map[string]any{
									"type": "object",
									"description": "Target-specific options dictionary (string values). Write ONLY the keys your target needs; leave the whole dictionary empty for caller:*, which already knows its destination. " +
										"For " + targetsAccepting("task_id") + " — task_id: required, [a-z0-9_-]+, reusing the same task_id targets the existing thread; agent: template name, empty for the session default (never invent placeholders like \"default\"); provider+model: optional model override, must be set together, list valid pairs via `set-model --list-fallback`. " +
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

	// dispatch({}) is the one form that ends the turn: the model explicitly
	// chose to say nothing. It halts only when it is the sole tool call in the
	// message (batched, halting would discard the sibling tools' results), so
	// a batched empty dispatch is a no-op. Batch size 0 means the caller didn't
	// plumb the context (tests, direct invocation): treat as solo.
	if len(a.Sends) == 0 {
		if provider.ToolBatchSizeFromContext(ctx) > 1 {
			return toolResult("dispatch", map[string]any{
				"outcome": "no-op",
			}, "Nothing sent and the turn was NOT ended: dispatch({}) only ends the turn when it is the sole tool call in your message, and this call was batched with other tool calls. Continue working with their results; when finished, write your reply or call dispatch({}) alone to end silently.")
		}
		t.host.SignalHalt()
		return toolResult("dispatch", map[string]any{
			"outcome": "turn-terminated-silent",
		}, "Turn terminated silently. No delivery; history recorded.")
	}

	// Every routing dispatch is asynchronous: the sends go out and the turn
	// continues. Content written alongside the call is an ordinary
	// intermediate message and the turn's final message speaks for it, so
	// there is nothing to settle here.

	// Validate entire batch first (all-or-nothing on validation): a batch that
	// half-delivers and then reports an error is worse than one that delivers
	// nothing, because executed sends cannot be unrolled.
	if errs := t.validateAll(a.Sends); len(errs) > 0 {
		return buildDispatchErrorResult(errs)
	}

	// Execute. Partial failure possible: executed deliveries cannot be unrolled.
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
		return fmt.Sprintf("unknown to: %q (must be one of %s). To speak to your own human, do not dispatch at all — just write your message as your reply text.",
			send.To, strings.Join(dispatchTargetNames(), "/"))
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
	case TargetSubagent, TargetSubagentFork:
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
			// Named by KEY SHAPE, not by the targets that produce it: the
			// infixes are what this actually checks, and they do not move when
			// a target is renamed.
			if strings.Contains(uid, ":threads:") || strings.Contains(uid, ":fork:") {
				return "user_id cannot address child sessions (keys containing :threads: or :fork:)"
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
	case TargetSubagentFork:
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
	case TargetSubagentFork:
		p := send.Params
		key, note, err := t.host.CreateOrWakeFork(ctx, p["agent"], p["task_id"], send.Body, p["provider"], p["model"])
		if err != nil {
			return ExecutedItem{}, err
		}
		return ExecutedItem{To: TargetSubagentFork, SessionKey: key, Note: note}, nil
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
	case TargetSubagentFork:
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
	sb.WriteString("Validation failed: no sends were executed. Fix and re-call dispatch.\n\nErrors:\n")
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

// turnContinuesNote closes every result that delivered something. The
// deliveries above it are real (the model must not resend them) and the turn
// is still running, so the model still owes its own reader the final word.
const turnContinuesNote = "The turn continues: dispatch never waits for the target, and the deliveries above are already sent (do NOT resend them). " +
	"A subagent/fork you dispatched notifies you automatically with a `progress` wake when its turn ends; do not wait or poll for it. " +
	"Now finish this turn: tell your human what you just did in plain reply text, or call dispatch({}) alone if there is nothing worth saying."

func buildDispatchSuccessResult(executed []ExecutedItem) string {
	var sb strings.Builder
	if len(executed) == 1 {
		sb.WriteString("Executed 1 send.\n\n")
	} else {
		fmt.Fprintf(&sb, "Executed %d sends.\n\n", len(executed))
	}
	for i, ex := range executed {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, describeExecuted(ex))
	}
	sb.WriteString("\n")
	sb.WriteString(turnContinuesNote)
	return toolResult("dispatch", map[string]any{
		"outcome": "delivered",
	}, sb.String())
}

func buildDispatchMixedResult(executed []ExecutedItem, errs []DispatchError) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Partial failure: %d send(s) executed, %d failed. Executed deliveries cannot be unrolled.\n", len(executed), len(errs))
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
	sb.WriteString("\n")
	sb.WriteString(turnContinuesNote)
	return toolResult("dispatch", map[string]any{
		"outcome": "partial-failure",
	}, sb.String())
}
