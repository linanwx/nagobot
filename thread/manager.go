package thread

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
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
		if t.state == ThreadIdle && t.hasMessages() {
			t.state = ThreadRunning

			go func(thread *Thread) {
				// Acquire concurrency slot (may block).
				sem <- struct{}{}
				defer func() { <-sem }()

				thread.RunOnce(ctx)

				m.mu.Lock()
				thread.state = ThreadIdle
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
	t := m.NewThread(sessionKey, msg.AgentName)
	t.Enqueue(msg)
	m.notify()
}

// WakeWith is a convenience method that constructs a WakeMessage from simple
// parameters. Satisfies the tools.ThreadWaker interface.
func (m *Manager) WakeWith(sessionKey, source, message string) {
	m.Wake(sessionKey, &WakeMessage{
		Source:  source,
		Message: message,
	})
}

// NewThread returns an existing thread, or creates one with the given agent name.
func (m *Manager) NewThread(sessionKey, agentName string) *Thread {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "channel:default"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.threads[sessionKey]; ok {
		return t
	}

	t := &Thread{
		id:         fmt.Sprintf("thread-%d", time.Now().UnixNano()),
		mgr:        m,
		sessionKey: strings.TrimSpace(sessionKey),
		state:      ThreadIdle,
		inbox:      make(chan *WakeMessage, defaultInboxSize),
		signal:     m.signal,
	}
	t.Agent = m.cfg.Agents.New(agentName)
	t.provider = m.cfg.DefaultProvider
	t.tools = t.buildTools()
	t.RegisterHook(t.contextPressureHook())
	m.threads[sessionKey] = t
	return t
}
