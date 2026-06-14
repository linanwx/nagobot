package thread

import (
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

// Sink is an alias for msg.Sink.
type Sink = msg.Sink

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
	WakeRephrase     = msg.WakeRephrase
	WakePreThink     = msg.WakePreThink
	WakeAudioPreview = msg.WakeAudioPreview
	WakeImagePreview = msg.WakeImagePreview
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

	// Tier 1: mechanical tool-result compression (idle ≥5 min, no token threshold)
	tier1IdleMin = 5 * time.Minute

	// During an active (non-idle) conversation the idle scan never fires, so
	// Tier 1 is also forced at turn-end once this many user messages have
	// accumulated since the last Tier 1 run.
	forcedTier1UserMsgs = 5

	// Tier 2: AI-driven silent compression (idle ≥30 min, remaining < Tier2Token)
	tier2IdleMin = 30 * time.Minute
)

// ThreadConfig contains shared dependencies for creating threads.
type ThreadConfig struct {
	DefaultProvider     provider.Provider
	ProviderName        string
	ModelName           string
	Tools               *tools.Registry
	Skills              *skills.Registry
	Agents              *agent.AgentRegistry
	Workspace           string
	SkillsDir           string
	BuiltinSkillsDir    string
	SessionsDir         string
	ContextWindowTokens int
	MaxCompletionTokens int
	Sessions            *session.Manager
	DefaultSinkFor      func(sessionKey string) Sink
	DefaultAgentFor     func(sessionKey string) string // Session key → default agent name
	HealthChannelsFn    func() *tools.HealthChannelsInfo
	ProviderFactory     *provider.Factory                       // For per-agent model routing
	Models              []config.ModelRule                      // Typed model-routing rules (startup snapshot)
	ModelsFn            func() []config.ModelRule               // Hot-reload: returns latest model rules from config
	DefaultModelFn      func() (providerName, modelName string) // Hot-reload: returns latest default provider/model from config
	SessionTimezoneFor  func(sessionKey string) string          // Session key → IANA timezone
	MetricsStore        *monitor.Store                          // Turn metrics storage (optional)
	Sections            *agent.SectionRegistry                  // Shared section registry for prompt assembly
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

	mu               sync.Mutex
	hooks            []turnHook
	postHooks        []postTurnHook // Hooks run after each turn; returned messages are appended to session.jsonl.
	pending          []*WakeMessage // Non-mergeable messages deferred by tryMerge (avoids channel requeue deadlock).
	defaultSink      Sink           // Fallback sink when WakeMessage.Sink is nil.
	lastActiveAt     time.Time      // Last time this thread completed work (used by GC).
	lastUserActiveAt time.Time      // Last time a real user interacted (used by compression).
	lastWakeSource   msg.WakeSource // Source of the most recent wake (set at RunOnce start).
	suppressSink     bool           // When true, RunOnce skips sink delivery (reset after each turn).
	haltLoop         bool           // When true, Runner stops after current tool calls complete.
	currentSink      Sink           // Current turn's active sink (set by run(), cleared on turn end). Used by dispatch(to=caller:*).
	currentCallerKey string         // Caller session key for the current wake; empty for user/system wakes.

	execMetrics           *ExecMetrics // Non-nil only while a turn is executing.
	lastCompressAttemptAt time.Time    // Last time tier 2 compression was enqueued (prevents duplicate enqueue).
	lastCompressedAt      time.Time    // Last time tier 2 compression completed successfully.

	lastTurnMsgCount   int // Number of wake messages merged into the just-run turn (set by tryMerge).
	userMsgsSinceTier1 int // Count of user messages since the last Tier 1 run; forces Tier 1 at turn-end once it reaches forcedTier1UserMsgs.

	memoryIndexCache   string    // Cached buildMemoryIndexSection result.
	memoryIndexModTime time.Time // Directory modtime when cache was built.
}

// ToolCallRecord is an alias for msg.ToolCallRecord.
type ToolCallRecord = msg.ToolCallRecord

// ExecMetrics tracks real-time execution metrics for a running turn.
type ExecMetrics struct {
	mu             sync.Mutex
	TurnStart      time.Time
	Iterations     int
	TotalToolCalls int
	CurrentTool    string // empty when not executing a tool
	ToolCalls      []ToolCallRecord

	// Last-turn token data — overwritten (not accumulated) each LLM call by the runner.
	PromptEstimated      int
	CompletionEstimated  int
	ReasoningEstimated   int
	LastPromptActual     int
	LastCompletionActual int
	LastTotalActual      int
	LastCachedActual     int
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
	cfg := t.cfg()
	if cfg.SessionTimezoneFor != nil {
		if tz := cfg.SessionTimezoneFor(t.sessionKey); tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				return loc
			}
		}
	}
	return time.Now().Location()
}
