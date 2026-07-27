// Package msg defines the WakeMessage type and the YAML-frontmatter helpers
// shared across thread, tools, session, and cmd. All YAML frontmatter
// construction and parsing in this codebase MUST go through frontmatter.go;
// see that file for the rationale.
package msg

import (
	"context"
	"strings"
	"time"
)

// BuildSystemMessage constructs a standardized system message using YAML
// frontmatter. The output begins with `type` and `sender: system`, then the
// remaining fields in sorted order, then a blank line and the trimmed body.
func BuildSystemMessage(msgType string, fields map[string]string, content string) string {
	extras := make(map[string]any, len(fields))
	for k, v := range fields {
		extras[k] = v
	}
	leading := [][2]string{
		{"type", msgType},
		{"sender", "system"},
	}
	mapping, err := SortedFieldsMapping(leading, extras)
	if err != nil {
		return ""
	}

	body := ""
	if trimmed := strings.TrimSpace(content); trimmed != "" {
		body = "\n" + trimmed
	}
	return BuildFrontmatter(mapping, body)
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

// ToolCallRecord records a single tool invocation during a turn.
type ToolCallRecord struct {
	Name          string `json:"name"`
	ArgsSummary   string `json:"args"`       // first 500 runes of arguments JSON
	ResultPreview string `json:"result"`     // first 500 runes of tool result
	DurationMs    int64  `json:"durationMs"` // execution time in milliseconds
	Error         bool   `json:"error,omitempty"`
}

// ThreadInfo holds the summary status of a thread.
type ThreadInfo struct {
	ID         string `json:"id"`
	SessionKey string `json:"sessionKey"`
	State      string `json:"state"` // "running", "pending", "idle"
	Pending    int    `json:"pending"`
	// Runtime metrics (only populated when state=running).
	Iterations       int              `json:"iterations,omitempty"`
	TotalToolCalls   int              `json:"totalToolCalls,omitempty"`
	CurrentTool      string           `json:"currentTool,omitempty"`
	ElapsedSec       int              `json:"elapsedSec,omitempty"`
	ToolTrace        []ToolCallRecord `json:"toolTrace,omitempty"`
	LastUserActiveAt time.Time        `json:"lastUserActiveAt,omitempty"`
	// Progress-reporting context (only populated when state=running).
	TurnWakeSource string    `json:"turnWakeSource,omitempty"` // wake source of the currently running turn
	OriginRequest  string    `json:"originRequest,omitempty"`  // trimmed wake body of the running turn (frontmatter stripped)
	TurnStart      time.Time `json:"turnStart,omitzero"`       // start time of the running turn (identifies the turn across scans)
}

// WakeSource identifies how a thread was woken.
type WakeSource string

const (
	WakeTelegram     WakeSource = "telegram"
	WakeWeb          WakeSource = "web"
	WakeDiscord      WakeSource = "discord"
	WakeFeishu       WakeSource = "feishu"
	WakeWeCom        WakeSource = "wecom"
	WakeSocket       WakeSource = "socket"
	WakeSession      WakeSource = "session" // another session woke us; caller in WakeMessage.CallerSessionKey
	WakeCron         WakeSource = "cron"
	WakeCompression  WakeSource = "compression"
	WakeHeartbeat    WakeSource = "heartbeat"
	WakeResume       WakeSource = "resume"
	WakeAudioPreview WakeSource = "audiopreview"
	WakeImagePreview WakeSource = "imagepreview"
	WakeProgress     WakeSource = "progress"        // progress scanner delivering a running child's AI-generated progress summary to its user-facing ancestor
	WakeProgressSum  WakeSource = "progresssummary" // progress scanner asking the progress-summary sibling agent to summarize a running turn's tool activity
	WakeQuote        WakeSource = "quote"           // a client asking the quote sibling agent to condense a message into a one-line markdown quote
	WakePin          WakeSource = "pin"             // a client asking the pin sibling agent to file a message into the parent session's pins/ directory
)

// IsUserVisibleSource reports whether the given source represents a real
// user-initiated channel (telegram, discord, cli, web, feishu).
func IsUserVisibleSource(source WakeSource) bool {
	switch source {
	case WakeTelegram, WakeDiscord, WakeWeb, WakeFeishu, WakeWeCom, WakeSocket:
		return true
	}
	return false
}

// RequiresExplicitDispatch reports whether a turn woken by this source must
// terminate via the dispatch tool — naive text (no tool calls) is rejected
// and the runner forces another iteration with a system reminder injected.
//
// Why: for these wake sources the destination of a naive reply is ambiguous
// (e.g. WakeSession could mean reply-to-peer, alert-user, or both) and the
// implicit auto-route is too easy to misuse. Forcing dispatch makes the
// model's routing intent explicit and auditable.
func (s WakeSource) RequiresExplicitDispatch() bool {
	switch s {
	case WakeSession:
		// Includes both "peer session asked me a question" and
		// "child subagent/fork reported back" — both arrive as WakeSession.
		return true
	}
	return false
}

// CallerKind is the high-level classification of a wake's caller, used by
// dispatch's caller:session validation.
type CallerKind string

const (
	CallerKindNone    CallerKind = ""        // no active caller (edge case — no wake source set)
	CallerKindUser    CallerKind = "user"    // caller is the channel user (user-channel wake)
	CallerKindSession CallerKind = "session" // caller is another session (WakeSession)
	CallerKindSystem  CallerKind = "system"  // caller is system automation (cron/heartbeat/compression/resume)
)

// CallerKindFromSource maps a wake source to the caller kind.
func CallerKindFromSource(source WakeSource) CallerKind {
	if IsUserVisibleSource(source) {
		return CallerKindUser
	}
	if source == WakeSession {
		return CallerKindSession
	}
	return CallerKindSystem
}

// WakeMessage is an item in a thread's wake queue.
type WakeMessage struct {
	Source           WakeSource            // Wake source.
	Message          string                // Wake payload text.
	ID               string                // Optional client-supplied message id, validated at the channel boundary. Persisted verbatim as the user message's ID so the sender can address the message by the same id before and after it lands on disk. Empty = the session store assigns one.
	Media            []string              // Optional media markers (<<media:mime:path>>) attached to the first user message of this wake, delivered natively in user content (no read_file). Dropped with a warning if the resolved model lacks the matching capability.
	Sinks            SinkSet               // The channel this wake arrived on. Unioned over the session's own destinations at RunOnce — it does not replace them.
	CallerSink       SessionSink           // Where a reply to WHOEVER WOKE US goes (dispatch(to=caller:session), SendToCaller). One place, never broadcast: the caller is a property of this wake, not a view of the session.
	MessageSink      MessageSink           // Per-wake message-specific delivery (reactions on the source message). Zero value = nothing message-specific.
	AgentName        string                // Optional agent name override for this wake.
	OverrideProvider string                // Optional model override (subagent/fork dispatch only): provider name. Set together with OverrideModel; applied per-wake at highest routing precedence.
	OverrideModel    string                // Optional model override (subagent/fork dispatch only): model type. Set together with OverrideProvider.
	Vars             map[string]string     // Optional vars override for this wake.
	Sender           string                // Optional sender override.
	SenderName       string                // Optional human sender display name (e.g. group-chat username). Rendered as `sender_name` in the wake frontmatter.
	SenderID         string                // Optional stable sender identity ("discord:1480..." or "person:p_xxx" for authenticated web users). Rendered as `sender_id` in the wake frontmatter; the web UI aligns "me" messages with it.
	MediaInfo        string                // Optional media resource summary from the channel ("[Media: photo] image_path: …"). Rendered one-line as `media` in the wake frontmatter.
	MediaPreview     string                // Optional upfront media preview (image description / audio transcription). Rendered one-line as `media_preview` in the wake frontmatter.
	CallerSessionKey string                // For Source=WakeSession: the session that woke us. Empty otherwise.
	RecentChat       string                // Optional recent chat history (rendered into the wake payload markdown body).
	OnComplete       func(response string) // Called after the turn completes with the full response text.
	EnqueuedAt       time.Time             // Set by Thread.Enqueue if zero. Used as the wake `time` field so the LLM sees enqueue time, not processing time.

	// Traceparent carries the W3C span context of whatever produced this wake,
	// so the turn it eventually runs is a child of that span rather than a
	// disconnected root.
	//
	// It is a FIELD rather than a context.Context because Manager.Wake takes no
	// ctx — the inbox is a hard context break, and RunOnce receives the
	// manager's long-lived ctx, not the message's. Serializing to the standard
	// traceparent string keeps this working for every producer at once: channel
	// messages, cron injects, subagent/fork spawns, sibling sessions (quote,
	// pin, progress-summary) and peer dispatch all reach a thread through Wake.
	Traceparent string

	// MergedTraceparents holds the traceparents of the OTHER wakes tryMerge
	// folded into this one. Set by tryMerge, read once when the turn span opens.
	//
	// They become span LINKS, not parents: N messages collapse into one turn, so
	// parent-child would force picking one arbitrary origin and silently drop
	// the causality of the rest.
	MergedTraceparents []string
}

// SettleOutcome names what happened to assistant content emitted alongside a
// dispatch call. It is an enum rather than a prose string because the dispatch
// tool renders a DIFFERENT note to the model for each value — a single
// "this reached nobody" message was wrong on three of the four.
type SettleOutcome string

const (
	// SettleNoReader — this turn has no destination for plain content at all
	// (heartbeat / compression, or a session whose sinks are empty). The text
	// genuinely went nowhere and only a send body can carry it.
	SettleNoReader SettleOutcome = "no-reader"

	// SettleTurnContinues — a batched dispatch, or dispatch({}). The turn is
	// still running, so the model's eventual final message is what speaks; this
	// intermediate text was simply not the delivery. Nothing is lost by leaving
	// it, and repeating it in a send body would say everything twice.
	SettleTurnContinues SettleOutcome = "turn-continues"

	// SettleAlreadySentToCaller — an executed to=caller:session already wrote to
	// the very destination this content would take. Only sessions with no human
	// of their own reach this: there, contentSink and the caller sink are the
	// same reader, and that reader did get the news — in the send's body.
	SettleAlreadySentToCaller SettleOutcome = "already-sent-to-caller"

	// SettleDeliveryFailed — a destination existed and the send returned an
	// error. Distinct from the three above: nothing about the turn's shape is
	// wrong, the transport failed.
	SettleDeliveryFailed SettleOutcome = "delivery-failed"
)
