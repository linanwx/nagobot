package thread

import (
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/tools"
)

// SessionSink is an alias for msg.SessionSink.
type SessionSink = msg.SessionSink

// SinkSet is an alias for msg.SinkSet.
type SinkSet = msg.SinkSet

// MessageSink is an alias for msg.MessageSink.
type MessageSink = msg.MessageSink

// NewSinks is a convenience re-export of msg.NewSinks.
var NewSinks = msg.NewSinks

// StreamEvent is an alias for msg.StreamEvent.
type StreamEvent = msg.StreamEvent

// NewStreamPipe is a convenience re-export of msg.NewStreamPipe.
var NewStreamPipe = msg.NewStreamPipe

// ReactFunc is an alias for msg.ReactFunc.
type ReactFunc = msg.ReactFunc

// ReactEvent is an alias for msg.ReactEvent.
type ReactEvent = msg.ReactEvent

// React event constants re-exported from msg package.
const (
	ReactToolCalls = msg.ReactToolCalls
	ReactStreaming = msg.ReactStreaming
)

// NewReactFunc is a convenience re-export of msg.NewReactFunc.
var NewReactFunc = msg.NewReactFunc

// WakeMessage is an alias for msg.WakeMessage.
type WakeMessage = msg.WakeMessage

// WakeSource is an alias for msg.WakeSource.
type WakeSource = msg.WakeSource

// Wake source constants re-exported from msg package.
const (
	WakeTelegram     = msg.WakeTelegram
	WakeWeb          = msg.WakeWeb
	WakeDiscord      = msg.WakeDiscord
	WakeFeishu       = msg.WakeFeishu
	WakeWeCom        = msg.WakeWeCom
	WakeSession      = msg.WakeSession
	WakeCron         = msg.WakeCron
	WakeCompression  = msg.WakeCompression
	WakeHeartbeat    = msg.WakeHeartbeat
	WakeResume       = msg.WakeResume
	WakeAudioPreview = msg.WakeAudioPreview
	WakeImagePreview = msg.WakeImagePreview
	WakeProgress     = msg.WakeProgress
	WakeProgressSum  = msg.WakeProgressSum
	WakeQuote        = msg.WakeQuote
	WakePin          = msg.WakePin
)

// threadState represents the runtime state of a thread.
type threadState int

const (
	threadIdle    threadState = iota // No pending messages.
	threadRunning                    // Currently executing.
)

const (
	defaultMaxConcurrency  = 16
	defaultInboxSize       = 64
	defaultInjectInboxSize = 16
	defaultThreadTTL       = 3 * time.Hour
	gcInterval             = 5 * time.Minute
	streamFlushThreshold   = 600 // minimum unsent bytes before attempting a streamer split

	// Tier 1 (and the lossy slide_window tier): mechanical rewrites of already-sent
	// history. They wait until the provider's prompt cache has gone cold: the
	// openai-oauth cache only reuses the full prefix of an earlier request, so
	// rewriting even the last few messages discards every cached prefix. Measured
	// on kingsley, a prefix with no rewrite survived every 5-45 min gap and about
	// half of 45-60 min ones, in line with OpenAI's documented retention of up to
	// one hour.
	tier1IdleMin = 60 * time.Minute

	// Tier 2: AI-driven silent compression (remaining < Tier2Token). It replaces
	// the history wholesale, so it waits for a cold cache too, and runs after
	// Tier 1 in the same scan so it only fires if Tier 1 was not enough.
	tier2IdleMin = tier1IdleMin
)

// ThreadConfig contains shared dependencies for creating threads.
type ThreadConfig struct {
	DefaultProvider       provider.Provider
	ProviderName          string
	ModelName             string
	Tools                 *tools.Registry
	Skills                *skills.Registry
	Agents                *agent.AgentRegistry
	Workspace             string
	SkillsDir             string
	BuiltinSkillsDir      string
	SessionsDir           string
	ContextWindowTokens   int
	MaxCompletionTokens   int
	Sessions              *session.Manager
	DefaultSinkFor        func(sessionKey string) SinkSet
	DefaultAgentFor       func(sessionKey string) string // Session key → default agent name
	HealthChannelsFn      func() *tools.HealthChannelsInfo
	ProviderFactory       *provider.Factory                       // For per-agent model routing
	Models                []config.ModelRule                      // Typed model-routing rules (startup snapshot)
	ModelsFn              func() []config.ModelRule               // Hot-reload: returns latest model rules from config
	DefaultModelFn        func() (providerName, modelName string) // Hot-reload: returns latest default provider/model from config
	CompressionPercentsFn func() (tier2, tier3 int)               // Hot-reload: Tier 2 / Tier 3 trigger percents from config
	SessionTimezoneFor    func(sessionKey string) string          // Session key → IANA timezone
	MetricsStore          *monitor.Store                          // Turn metrics storage (optional)
	Sections              *agent.SectionRegistry                  // Shared section registry for prompt assembly
}

// Thread is a single execution unit with an agent, wake queue, and optional session.
type Thread struct {
	id  string
	mgr *Manager
	*agent.Agent

	sessionKey string
	provider   provider.Provider
	tools      *tools.Registry

	// State machine fields.
	state       threadState
	inbox       chan *WakeMessage // Buffered wake queue.
	injectInbox chan string       // Dedicated control/injection lane (bypasses tryMerge/canMerge).
	signal      chan struct{}     // Shared with Manager for notification.

	mu                    sync.Mutex
	hooks                 []turnHook
	postHooks             []postTurnHook // Hooks run after each turn; returned messages are appended to session.jsonl.
	pending               []*WakeMessage // Non-mergeable messages deferred by tryMerge (avoids channel requeue deadlock).
	defaultSink           SinkSet        // Fallback sinks when the wake carries none.
	lastActiveAt          time.Time      // Last time this thread completed work (used by GC).
	lastUserActiveAt      time.Time      // Last time a real user interacted (used by compression).
	lastWakeSource        msg.WakeSource // Source of the most recent wake (set at RunOnce start).
	modelOverrideProvider string         // Per-wake model override provider (from dispatch subagent/fork); set at RunOnce start, empty when none. Highest routing precedence.
	modelOverrideModel    string         // Per-wake model override model type; paired with modelOverrideProvider.
	suppressSink          bool           // When true, RunOnce skips sink delivery (reset after each turn).
	haltLoop              bool           // When true, Runner stops after current tool calls complete.
	dispatchedThisTurn    bool           // Set when a dispatch send executed this turn; reset at turn start.
	currentSink           SinkSet        // Current turn's session destinations (set by run(), cleared on turn end).
	currentCallerSink     SessionSink    // Current turn's reply-to-caller destination (set by run(), cleared on turn end). One place, never broadcast. Used by dispatch(to=caller:*).
	currentCallerKey      string         // Caller session key for the current wake; empty for user/system wakes.

	execMetrics            *ExecMetrics // Non-nil only while a turn is executing.
	lastTurnMetrics        *ExecMetrics // Metrics of the turn that just ended; read by RunOnce for the turn-end hook.
	lastCompressAttemptAt  time.Time    // Last time tier 2 compression was enqueued (prevents duplicate enqueue).
	lastCompressedAt       time.Time    // Last time tier 2 compression completed successfully.
	lastSystemPromptTokens int          // Estimated tokens of the last turn's system prompt; counted by Tier 2/3 so they size the request the trim sees.

	memoryIndexCache   string    // Cached buildMemoryIndexSection result.
	memoryIndexModTime time.Time // Directory modtime when cache was built.
}

// ToolCallRecord is an alias for msg.ToolCallRecord.
type ToolCallRecord = msg.ToolCallRecord

// ExecMetrics tracks real-time execution metrics for a running turn.
type ExecMetrics struct {
	mu             sync.Mutex
	TurnStart      time.Time
	OriginRequest  string // trimmed wake body that started the turn (frontmatter stripped); immutable after creation
	Iterations     int
	TotalToolCalls int
	CurrentTool    string // empty when not executing a tool
	ToolCalls      []ToolCallRecord

	// LastTextDeltaAt is when this turn last emitted USER-VISIBLE text. It is
	// what lets the progress scanner tell "waiting in silence" from "the answer
	// is already arriving on screen"; seeing text at all cannot, because a
	// preamble may still be followed by tool calls in the same message.
	// Zero when the turn has streamed no text — including every non-streaming
	// provider, which reports nothing and must therefore change no behaviour.
	LastTextDeltaAt time.Time

	// What the turn is doing right now, read by progress snapshots so the
	// session waiting on it can tell a child writing its answer from one that
	// is stuck. LLMCallStart is non-zero only while a model call is in flight;
	// the three fields after it describe that call and are reset when it
	// starts. CurrentToolSince is when CurrentTool started.
	LLMCallStart     time.Time
	LastReasoningAt  time.Time
	ReplyRunes       int
	ToolCallDraft    string // first tool the in-flight call started to emit
	CurrentToolSince time.Time

	// Last-turn token data — overwritten (not accumulated) each LLM call by the runner.
	PromptEstimated      int
	CompletionEstimated  int
	ReasoningEstimated   int
	LastPromptActual     int
	LastCompletionActual int
	LastTotalActual      int
	LastCachedActual     int
	LastCacheWriteActual int
	LastReasoningActual  int
	Media                MediaBreakdown
}

// StartIteration increments the iteration counter and clears the current tool.
func (m *ExecMetrics) StartIteration() {
	m.mu.Lock()
	m.Iterations++
	m.CurrentTool = ""
	m.mu.Unlock()
}

// SetCurrentTool records the tool currently being executed.
func (m *ExecMetrics) SetCurrentTool(name string) {
	m.mu.Lock()
	m.CurrentTool = name
	m.CurrentToolSince = time.Now()
	m.mu.Unlock()
}

// BeginLLMCall marks a model call as in flight and clears what the previous
// call left behind.
func (m *ExecMetrics) BeginLLMCall(at time.Time) {
	m.mu.Lock()
	m.LLMCallStart = at
	m.LastReasoningAt = time.Time{}
	m.ReplyRunes = 0
	m.ToolCallDraft = ""
	m.mu.Unlock()
}

// EndLLMCall marks the in-flight model call as finished.
func (m *ExecMetrics) EndLLMCall() {
	m.mu.Lock()
	m.LLMCallStart = time.Time{}
	m.mu.Unlock()
}

// NoteReasoningDelta stamps reasoning output of the in-flight call. It is
// kept apart from LastTextDeltaAt on purpose: see NoteTextDelta.
func (m *ExecMetrics) NoteReasoningDelta(at time.Time) {
	m.mu.Lock()
	m.LastReasoningAt = at
	m.mu.Unlock()
}

// NoteToolCallDraft records that the in-flight call started emitting a tool
// call. Providers report only its start, not its arguments.
func (m *ExecMetrics) NoteToolCallDraft(name string) {
	m.mu.Lock()
	if m.ToolCallDraft == "" {
		m.ToolCallDraft = name
	}
	m.mu.Unlock()
}

// NoteTextDelta stamps the arrival of a user-visible text delta.
//
// Deliberately NOT called for reasoning deltas: a long think is exactly when a
// progress note is worth most, since nothing has reached the user yet. The lock
// is taken on every delta rather than throttled — deltas arrive tens per second
// against a mutex read once per 30s scan, so there is no contention to save.
func (m *ExecMetrics) NoteTextDelta(at time.Time, runes int) {
	m.mu.Lock()
	m.LastTextDeltaAt = at
	m.ReplyRunes += runes
	m.mu.Unlock()
}

// RecordToolCall appends a tool call record and clears the current tool.
func (m *ExecMetrics) RecordToolCall(record ToolCallRecord) {
	m.mu.Lock()
	m.TotalToolCalls++
	m.ToolCalls = append(m.ToolCalls, record)
	m.CurrentTool = ""
	m.mu.Unlock()
}

// cfg returns the shared config from the manager.
func (t *Thread) cfg() *ThreadConfig {
	if t.mgr != nil {
		return t.mgr.cfg
	}
	return &ThreadConfig{}
}

// location returns the *time.Location for this thread's session timezone.
// Falls back to the system local timezone if not configured or invalid.
func (t *Thread) location() *time.Location {
	if t.mgr != nil {
		return t.mgr.locationFor(t.sessionKey)
	}
	return time.Now().Location()
}

// locationFor returns the *time.Location for a session key, by precedence:
//
//  1. the client-reported timezone persisted in the session's meta.json — the
//     device the human is actually on (currently only the web UI reports it);
//  2. the server-side channels.sessionTimezones config for the key;
//  3. the server's own system timezone.
//
// The client zone wins over the config zone deliberately: the human's own device
// is a better source of truth for "what wall-clock time is it for them" than an
// operator's per-session setting. Each stage LoadLocation-guards, so a stale or
// malformed value silently falls through instead of erroring. Shared by
// Thread.location and Manager-level helpers (e.g. media-preview rendering).
func (m *Manager) locationFor(sessionKey string) *time.Location {
	if m == nil {
		return time.Now().Location()
	}
	if dir := m.SessionDir(sessionKey); dir != "" {
		if tz := strings.TrimSpace(session.ReadMeta(dir).ClientTimezone); tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				return loc
			}
		}
	}
	if m.cfg != nil && m.cfg.SessionTimezoneFor != nil {
		if tz := m.cfg.SessionTimezoneFor(sessionKey); tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				return loc
			}
		}
	}
	return time.Now().Location()
}
