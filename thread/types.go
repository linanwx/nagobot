package thread

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/tools"
)

// Sink defines how thread output is delivered.
type Sink func(ctx context.Context, response string) error

// ProviderFactory creates provider instances for a provider/model pair.
type ProviderFactory func(providerName, modelType string) (provider.Provider, error)

// ThreadType marks the execution mode of a thread.
type ThreadType string

const (
	ThreadTypePlain   ThreadType = "plain"
	ThreadTypeChannel ThreadType = "channel"
	ThreadTypeChild   ThreadType = "child"
)

// Config contains shared dependencies for creating threads.
type Config struct {
	DefaultProvider     provider.Provider
	ProviderFactory     ProviderFactory
	Tools               *tools.Registry
	Skills              *skills.Registry
	Agents              *agent.AgentRegistry
	Workspace           string
	ContextWindowTokens int
	ContextWarnRatio    float64
	Sessions            *session.Manager
}

// Manager keeps long-lived threads (typically keyed by session key).
type Manager struct {
	cfg     *Config
	mu      sync.Mutex
	threads map[string]*ChannelThread
}

// NewManager creates a thread manager.
func NewManager(cfg *Config) *Manager {
	return &Manager{
		cfg:     cfg,
		threads: make(map[string]*ChannelThread),
	}
}

// GetOrCreate returns an existing thread for the session key, or creates one.
// Empty session keys always return a fresh stateless thread.
func (m *Manager) GetOrCreate(sessionKey string, ag *agent.Agent, sink Sink, sinkLabel string) *Thread {
	if strings.TrimSpace(sessionKey) == "" {
		return NewPlain(m.cfg, ag, sink, sinkLabel).Thread
	}
	return m.GetOrCreateChannel(sessionKey, ag, sink, sinkLabel).Thread
}

// GetOrCreateChannel returns an existing channel thread, or creates one.
func (m *Manager) GetOrCreateChannel(sessionKey string, ag *agent.Agent, sink Sink, sinkLabel string) *ChannelThread {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "channel:default"
	}
	sinkLabel = strings.TrimSpace(sinkLabel)

	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.threads[sessionKey]; ok {
		t.mu.Lock()
		if ag != nil {
			t.agent = ag
		}
		t.sink = sink
		t.sinkLabel = sinkLabel
		t.mu.Unlock()
		return t
	}

	t := NewChannel(m.cfg, ag, sessionKey, sink, sinkLabel)
	m.threads[sessionKey] = t
	return t
}

// Thread is a single execution unit (agent + runner + optional session + sink).
type Thread struct {
	id        string
	kind      ThreadType
	cfg       *Config
	agent     *agent.Agent
	provider  provider.Provider
	tools     *tools.Registry
	skills    *skills.Registry
	agents    *agent.AgentRegistry
	workspace string

	sessionKey string
	sink       Sink
	sinkLabel  string
	allowSpawn bool

	mu           sync.Mutex
	runMu        sync.Mutex
	hooks        []TurnHook
	children     map[string]*childState
	childCounter int64
}

// PlainThread is the base execution thread.
type PlainThread struct {
	*Thread
}

// ChannelThread composes PlainThread with channel/session semantics.
type ChannelThread struct {
	*PlainThread
}

// ChildThread composes PlainThread for delegated child work.
type ChildThread struct {
	*PlainThread
}

type childState struct {
	done   chan struct{}
	result string
	err    error
}

// NewPlain creates a stateless plain thread.
func NewPlain(cfg *Config, ag *agent.Agent, sink Sink, sinkLabel string) *PlainThread {
	return &PlainThread{
		Thread: newThread(cfg, ag, "", sink, sinkLabel, ThreadTypePlain, true),
	}
}

// NewChannel creates a channel-bound thread that can persist session state.
func NewChannel(cfg *Config, ag *agent.Agent, sessionKey string, sink Sink, sinkLabel string) *ChannelThread {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "channel:default"
	}
	return &ChannelThread{
		PlainThread: &PlainThread{
			Thread: newThread(cfg, ag, sessionKey, sink, sinkLabel, ThreadTypeChannel, true),
		},
	}
}

// NewChild creates a child thread. Child threads cannot spawn nested children.
func NewChild(cfg *Config, ag *agent.Agent, sink Sink) *ChildThread {
	return newChildWithSession(cfg, ag, sink, "")
}

func newChildWithSession(cfg *Config, ag *agent.Agent, sink Sink, sessionKey string) *ChildThread {
	return &ChildThread{
		PlainThread: &PlainThread{
			Thread: newThread(cfg, ag, sessionKey, sink, "", ThreadTypeChild, false),
		},
	}
}

// New keeps backward compatibility while preferring explicit constructors.
func New(cfg *Config, ag *agent.Agent, sessionKey string, sink Sink, sinkLabel string) *Thread {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return NewPlain(cfg, ag, sink, sinkLabel).Thread
	}
	return NewChannel(cfg, ag, sessionKey, sink, sinkLabel).Thread
}

func newThread(cfg *Config, ag *agent.Agent, sessionKey string, sink Sink, sinkLabel string, kind ThreadType, allowSpawn bool) *Thread {
	if cfg == nil {
		cfg = &Config{}
	}
	if ag == nil {
		ag = agent.NewRawAgent("default", "You are a helpful AI assistant.")
	}

	agentRegistry := cfg.Agents
	if agentRegistry == nil && cfg.Workspace != "" {
		agentRegistry = agent.NewRegistry(cfg.Workspace)
	}

	t := &Thread{
		id:         fmt.Sprintf("thread-%d", time.Now().UnixNano()),
		kind:       kind,
		cfg:        cfg,
		agent:      ag,
		provider:   cfg.DefaultProvider,
		tools:      cfg.Tools,
		skills:     cfg.Skills,
		agents:     agentRegistry,
		workspace:  cfg.Workspace,
		sessionKey: sessionKey,
		sink:       sink,
		sinkLabel:  strings.TrimSpace(sinkLabel),
		allowSpawn: allowSpawn,
		children:   make(map[string]*childState),
	}

	t.RegisterHook(t.contextPressureHook())
	return t
}

// Type returns the runtime thread type.
func (t *Thread) Type() ThreadType {
	return t.kind
}
