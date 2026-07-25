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

// Sink defines how thread output is delivered.
type Sink struct {
	Label     string
	Send      func(ctx context.Context, response string) error
	React     ReactFunc                       // Optional: fire-and-forget emoji reaction on the source message.
	Chunkable bool                            // True for sinks that accept chunked streaming delivery (telegram, discord, feishu, cli).
	Stream    func(ev StreamEvent)            // Optional: rich live streaming (thinking/text deltas, tool events). Must never block — back it with a StreamPipe. Sinks with Stream get final content via Send and skip chunked delivery.
	Flush     func(ctx context.Context) error // Optional: signals end-of-turn; recorders use this to commit buffered output.
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
	Media            []string              // Optional media markers (<<media:mime:path>>) attached to the first user message of this wake, delivered natively in user content (no read_file). Dropped with a warning if the resolved model lacks the matching capability.
	Sink             Sink                  // Per-wake sink. Zero value = no per-wake delivery.
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
}
