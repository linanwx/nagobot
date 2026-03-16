package thread

import (
	"sync"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/tools"
)

// Sink is an alias for msg.Sink.
type Sink = msg.Sink

// WakeMessage is an alias for msg.WakeMessage.
type WakeMessage = msg.WakeMessage

// WakeSource is an alias for msg.WakeSource.
type WakeSource = msg.WakeSource

// Wake source constants re-exported from msg package.
const (
	WakeTelegram       = msg.WakeTelegram
	WakeCLI            = msg.WakeCLI
	WakeWeb            = msg.WakeWeb
	WakeDiscord        = msg.WakeDiscord
	WakeFeishu         = msg.WakeFeishu
	WakeUserActive     = msg.WakeUserActive
	WakeChildTask      = msg.WakeChildTask
	WakeChildCompleted = msg.WakeChildCompleted
	WakeSleepCompleted = msg.WakeSleepCompleted
	WakeCron           = msg.WakeCron
	WakeCronFinished   = msg.WakeCronFinished
	WakeExternal       = msg.WakeExternal
	WakeCompression      = msg.WakeCompression
	WakeHeartbeatReflect = msg.WakeHeartbeatReflect
	WakeHeartbeatWake    = msg.WakeHeartbeatWake
)

// threadState represents the runtime state of a thread.
type threadState int

const (
	threadIdle    threadState = iota // No pending messages.
	threadRunning                    // Currently executing.
)

const (
	defaultMaxConcurrency = 16
	defaultInboxSize      = 64
	defaultThreadTTL      = 3 * time.Hour
	gcInterval            = 5 * time.Minute
	streamFlushThreshold  = 600 // minimum unsent bytes before attempting a streamer split

	// Tier 1: mechanical tool-result compression (idle 5-30 min, no token threshold)
	tier1IdleMin = 5 * time.Minute
	tier1IdleMax = 30 * time.Minute

	// Tier 2: AI-driven silent compression (idle ≥30 min, >65% tokens)
	tier2IdleMin    = 30 * time.Minute
	tier2IdleMax    = 24 * time.Hour
	tier2TokenRatio = 0.65
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
	ContextWarnRatio    float64
	Sessions            *session.Manager
	DefaultSinkFor      func(sessionKey string) Sink
	DefaultAgentFor     func(sessionKey string) string // Session key → default agent name
	HealthChannelsFn    func() *tools.HealthChannelsInfo
	ProviderFactory     *provider.Factory              // For per-agent model routing
	Models              map[string]*config.ModelConfig  // Model type → provider/model mapping (startup snapshot)
	ModelsFn            func() map[string]*config.ModelConfig // Hot-reload: returns latest Models from config
	AddJob              func(cron.Job) error           // Persistent job scheduling (for sleep_thread)
	SessionTimezoneFor  func(sessionKey string) string // Session key → IANA timezone
	MetricsStore        *monitor.Store                 // Turn metrics storage (optional)
}

// Thread is a single execution unit with an agent, wake queue, and optional session.
type Thread struct {
	id  string
	mgr *Manager
	*agent.Agent

	sessionKey string
	parent     *Thread // nil for root threads; set by SpawnChild
	provider   provider.Provider
	tools      *tools.Registry

	// State machine fields.
	state  threadState
	inbox  chan *WakeMessage // Buffered wake queue.
	signal chan struct{}     // Shared with Manager for notification.

	mu           sync.Mutex
	hooks        []turnHook
	pending      []*WakeMessage // Non-mergeable messages deferred by tryMerge (avoids channel requeue deadlock).
	defaultSink  Sink      // Fallback sink when WakeMessage.Sink is nil.
	lastActiveAt     time.Time      // Last time this thread completed work (used by GC).
	lastUserActiveAt time.Time      // Last time a real user interacted (used by compression).
	lastWakeSource   msg.WakeSource // Source of the most recent wake (set at RunOnce start).
	suppressSink bool      // When true, RunOnce skips sink delivery (reset after each turn).
	haltLoop     bool      // When true, Runner stops after current tool calls complete.

	execMetrics      *ExecMetrics // Non-nil only while a turn is executing.
	lastCompressAttemptAt time.Time // Last time tier 2 compression was enqueued (prevents duplicate enqueue).
	lastCompressedAt      time.Time // Last time tier 2 compression completed successfully.
}

// ToolCallRecord is an alias for msg.ToolCallRecord.
type ToolCallRecord = msg.ToolCallRecord

// ExecMetrics tracks real-time execution metrics for a running turn.
type ExecMetrics struct {
	mu             sync.Mutex
	TurnStart      time.Time
	Iterations     int
	TotalToolCalls int
	CurrentTool    string           // empty when not executing a tool
	ToolCalls      []ToolCallRecord
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

