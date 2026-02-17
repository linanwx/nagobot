package thread

import (
	"sync"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
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

// threadState represents the runtime state of a thread.
type threadState int

const (
	threadIdle    threadState = iota // No pending messages.
	threadRunning                    // Currently executing.
)

const (
	defaultMaxConcurrency = 16
	defaultInboxSize      = 64
	defaultThreadTTL      = 30 * time.Minute
	gcInterval            = 5 * time.Minute
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
	SessionsDir         string
	ContextWindowTokens int
	ContextWarnRatio    float64
	Sessions            *session.Manager
	DefaultSinkFor      func(sessionKey string) Sink
	DefaultAgentFor     func(sessionKey string) string // Session key → default agent name
	HealthChannels      *tools.HealthChannelsInfo
	ProviderFactory     *provider.Factory              // For per-agent model routing
	Models              map[string]*config.ModelConfig  // Model type → provider/model mapping
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
	state  threadState
	inbox  chan *WakeMessage // Buffered wake queue.
	signal chan struct{}     // Shared with Manager for notification.

	mu           sync.Mutex
	hooks        []turnHook
	defaultSink  Sink      // Fallback sink when WakeMessage.Sink is nil.
	lastActiveAt time.Time // Last time this thread completed work.

	execMetrics *ExecMetrics // Non-nil only while a turn is executing.
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

// cfg returns the shared config from the manager.
func (t *Thread) cfg() *ThreadConfig {
	if t.mgr != nil {
		return t.mgr.cfg
	}
	return &ThreadConfig{}
}
