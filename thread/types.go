package thread

import (
	"context"
	"sync"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/tools"
)

// Sink defines how thread output is delivered.
type Sink func(ctx context.Context, response string) error

// ThreadState represents the runtime state of a thread.
type ThreadState int

const (
	ThreadIdle    ThreadState = iota // No pending messages.
	ThreadRunning                    // Currently executing.
)

const (
	defaultMaxConcurrency = 16
	defaultInboxSize      = 64
)

// ThreadConfig contains shared dependencies for creating threads.
type ThreadConfig struct {
	DefaultProvider     provider.Provider
	Tools               *tools.Registry
	Skills              *skills.Registry
	Agents              *agent.AgentRegistry
	Workspace           string
	SkillsDir           string
	SessionsDir         string
	ContextWindowTokens int
	ContextWarnRatio    float64
	Sessions            *session.Manager
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
	state  ThreadState
	inbox  chan *WakeMessage // Buffered wake queue.
	signal chan struct{}     // Shared with Manager for notification.

	mu    sync.Mutex
	hooks []TurnHook
}

// cfg returns the shared config from the manager.
func (t *Thread) cfg() *ThreadConfig {
	if t.mgr != nil {
		return t.mgr.cfg
	}
	return &ThreadConfig{}
}
