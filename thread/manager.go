package thread

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/tools"
)

// Manager keeps long-lived threads and schedules their execution.
type Manager struct {
	cfg            *ThreadConfig
	mu             sync.Mutex
	threads        map[string]*Thread
	maxConcurrency int
	signal         chan struct{} // aggregated notification from all threads
}

// NewManager creates a thread manager.
func NewManager(cfg *ThreadConfig) *Manager {
	if cfg == nil {
		cfg = &ThreadConfig{}
	}
	return &Manager{
		cfg:            cfg,
		threads:        make(map[string]*Thread),
		maxConcurrency: defaultMaxConcurrency,
		signal:         make(chan struct{}, 1),
	}
}

// Run is the manager's main scheduling loop. It picks runnable threads and
// runs them up to maxConcurrency in parallel. Blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	sem := make(chan struct{}, m.maxConcurrency)
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.signal:
			m.scheduleReady(ctx, sem)
		}
	}
}

// scheduleReady scans threads and starts goroutines for any that are idle with
// pending messages.
func (m *Manager) scheduleReady(ctx context.Context, sem chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.threads {
		if t.state == threadIdle && t.hasMessages() {
			t.state = threadRunning

			go func(thread *Thread) {
				// Acquire concurrency slot (may block).
				sem <- struct{}{}
				defer func() { <-sem }()

				thread.RunOnce(ctx)

				m.mu.Lock()
				thread.state = threadIdle
				hasMore := thread.hasMessages()
				m.mu.Unlock()

				if hasMore {
					m.notify()
				}
			}(t)
		}
	}
}

// notify sends a non-blocking signal to the manager's run loop.
func (m *Manager) notify() {
	select {
	case m.signal <- struct{}{}:
	default:
	}
}

// Wake enqueues a wake message on the target thread (creating it if needed).
func (m *Manager) Wake(sessionKey string, msg *WakeMessage) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "channel:default"
	}
	t, err := m.NewThread(sessionKey, msg.AgentName)
	if err != nil {
		logger.Error("failed to create thread", "sessionKey", sessionKey, "agent", msg.AgentName, "err", err)
		return
	}
	t.Enqueue(msg)
	m.notify()
}

// NewThread returns an existing thread, or creates one with the given agent name.
func (m *Manager) NewThread(sessionKey, agentName string) (*Thread, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "channel:default"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.threads[sessionKey]; ok {
		return t, nil
	}

	t := &Thread{
		id:         fmt.Sprintf("thread-%d", time.Now().UnixNano()),
		mgr:        m,
		sessionKey: strings.TrimSpace(sessionKey),
		state:      threadIdle,
		inbox:      make(chan *WakeMessage, defaultInboxSize),
		signal:     m.signal,
	}
	a, err := m.cfg.Agents.New(agentName)
	if err != nil {
		return nil, err
	}
	t.Agent = a
	t.provider = m.cfg.DefaultProvider
	if m.cfg.DefaultSinkFor != nil {
		t.defaultSink = m.cfg.DefaultSinkFor(sessionKey)
	}
	t.tools = t.buildTools()
	t.registerHook(t.contextPressureHook())
	m.threads[sessionKey] = t
	return t, nil
}

// SetDefaultSinkFor configures a factory that returns the fallback sink for a given session key.
func (m *Manager) SetDefaultSinkFor(fn func(string) Sink) {
	m.cfg.DefaultSinkFor = fn
}

// RegisterTool adds a tool to the shared tool registry.
func (m *Manager) RegisterTool(t tools.Tool) {
	if m.cfg.Tools != nil {
		m.cfg.Tools.Register(t)
	}
}

// ThreadStatus returns the status of a thread by ID.
func (m *Manager) ThreadStatus(id string) (tools.ThreadInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.threads {
		if t.id == id {
			info := tools.ThreadInfo{ID: t.id}
			switch t.state {
			case threadRunning:
				info.State = "running"
				info.Pending = len(t.inbox)
			default:
				if t.hasMessages() {
					info.State = "pending"
					info.Pending = len(t.inbox)
				} else {
					info.State = "completed"
				}
			}
			return info, true
		}
	}
	return tools.ThreadInfo{}, false
}
