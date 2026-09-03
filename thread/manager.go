package thread

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread/msg"
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

// Shutdown performs cleanup of managed resources (e.g. flushes message counts).
func (m *Manager) Shutdown() {
	if m.cfg.Sessions != nil && m.cfg.Sessions.Counts != nil {
		m.cfg.Sessions.Counts.Stop()
	}
}

// Run is the manager's main scheduling loop. It picks runnable threads and
// runs them up to maxConcurrency in parallel. Blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	sem := make(chan struct{}, m.maxConcurrency)
	ticker := time.NewTicker(gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.signal:
			m.scheduleReady(ctx, sem)
		case <-ticker.C:
			m.gc()
			m.runCompressionScan()
		}
	}
}

// gc removes idle threads that have been inactive beyond the TTL.
func (m *Manager) gc() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, t := range m.threads {
		if t.state == threadIdle && !t.hasMessages() && time.Since(t.lastActiveAt) > defaultThreadTTL {
			delete(m.threads, key)
			logger.Debug("thread gc", "sessionKey", key, "threadID", t.id)
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
				sem <- struct{}{}
				defer func() {
					<-sem
					if r := recover(); r != nil {
						logger.Error("thread panic recovered",
							"threadID", thread.id,
							"sessionKey", thread.sessionKey,
							"panic", r,
							"stack", string(debug.Stack()),
						)
						thread.mu.Lock()
						thread.execMetrics = nil
						thread.mu.Unlock()
						m.mu.Lock()
						thread.lastActiveAt = time.Now()
						thread.state = threadIdle
						hasMore := thread.hasMessages()
						m.mu.Unlock()
						if hasMore {
							m.notify()
						}
					}
				}()

				thread.RunOnce(ctx)

				m.mu.Lock()
				now := time.Now()
				thread.lastActiveAt = now
				if msg.IsUserVisibleSource(thread.lastWakeSource) {
					thread.lastUserActiveAt = now
					thread.userMsgsSinceTier1 += thread.lastTurnMsgCount
				}
				if thread.lastWakeSource == WakeCompression {
					thread.lastCompressedAt = now
				}
				justEnded := thread.lastWakeSource
				// Force Tier 1 once enough user messages have accumulated since the
				// last run — keeps an active (never-idle) conversation compressed.
				forceTier1 := justEnded != WakeCompression && thread.userMsgsSinceTier1 >= forcedTier1UserMsgs
				thread.state = threadIdle
				hasMore := thread.hasMessages()
				m.mu.Unlock()

				// Turn-end forced Tier 1 (active conversation, no idle window).
				if forceTier1 {
					m.tryTier1Compress(thread.sessionKey)
				}

				// Turn-end Tier 3: if this turn grew context past the Tier 3
				// threshold, enqueue a separate background compression turn,
				// decoupled from the user's next message. Skipped after a
				// compression turn to avoid looping.
				if justEnded != WakeCompression {
					m.tryTier3Compress(thread.sessionKey)
				}

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
		sessionKey = "cli"
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
		sessionKey = "cli"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.threads[sessionKey]; ok {
		return t, nil
	}

	t := &Thread{
		id:               "thread-" + RandomHex(4),
		mgr:              m,
		sessionKey:       strings.TrimSpace(sessionKey),
		state:            threadIdle,
		inbox:            make(chan *WakeMessage, defaultInboxSize),
		injectInbox:      make(chan string, defaultInjectInboxSize),
		signal:           m.signal,
		lastActiveAt:     time.Now(),
		lastUserActiveAt: time.Now(),
	}
	if strings.TrimSpace(agentName) != "" {
		// Explicit agent from WakeMessage — persist to meta.json so it
		// survives thread GC and restarts.
		m.persistAgent(sessionKey, agentName)
	} else if m.cfg.DefaultAgentFor != nil {
		// No explicit agent — read from meta.json (falls back to "soul").
		agentName = m.cfg.DefaultAgentFor(sessionKey)
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
	t.registerHook(t.balanceWarningHook())
	m.threads[sessionKey] = t
	return t, nil
}

// SetDefaultSinkFor configures a factory that returns the fallback sink for a given session key.
func (m *Manager) SetDefaultSinkFor(fn func(string) SinkSet) {
	m.cfg.DefaultSinkFor = fn
}

// SetDefaultAgentFor configures a factory that returns the default agent name for a given session key.
func (m *Manager) SetDefaultAgentFor(fn func(string) string) {
	m.cfg.DefaultAgentFor = fn
}

// RegisterTool adds a tool to the shared tool registry.
func (m *Manager) RegisterTool(t tools.Tool) {
	if m.cfg.Tools != nil {
		m.cfg.Tools.Register(t)
	}
}

// stopSessionBody is the control message injected by StopSession. It is a soft
// stop: the running turn's LLM sees it at the next iteration boundary and is
// asked to terminate the turn itself. There is no hard cancellation — an
// in-flight tool/LLM call runs to completion before the boundary is reached.
const stopSessionBody = "OPERATOR STOP: an operator has requested that this session stop immediately. " +
	"Do NOT start any new tool calls or begin any new work. End this turn right now by calling " +
	"dispatch({}) (empty sends — silent termination). Abandon anything still in progress."

// InjectMessage pushes body onto the target thread's dedicated injection queue,
// bypassing the normal merge gate (canMerge). The message surfaces at the
// running turn's next iteration boundary. This is a general, internal primitive;
// it is intentionally NOT exposed as a general CLI command (only StopSession is)
// to avoid abuse. Returns a human-readable note, or an error if no thread is
// currently loaded for the key.
func (m *Manager) InjectMessage(sessionKey, body string) (string, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "", fmt.Errorf("session key is empty")
	}
	m.mu.Lock()
	t, ok := m.threads[sessionKey]
	running := ok && t.state == threadRunning
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no active thread for session %q (nothing running to inject into)", sessionKey)
	}
	if err := t.EnqueueInject(body); err != nil {
		return "", err
	}
	if running {
		return "injected into running thread; will surface at its next iteration boundary", nil
	}
	return "thread loaded but not currently running; injection queued for its next turn", nil
}

// StopSession soft-stops a session by injecting stopSessionBody. The session's
// LLM is asked to terminate the current turn via dispatch({}) at the next
// iteration boundary. Returns an error if no thread is loaded for the key.
func (m *Manager) StopSession(sessionKey string) (string, error) {
	return m.InjectMessage(sessionKey, stopSessionBody)
}

// HasThread reports whether a thread exists for the given session key.
func (m *Manager) HasThread(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.threads[key]
	return ok
}

// SessionDir returns the on-disk directory for a session key, or "" if unavailable.
func (m *Manager) SessionDir(key string) string {
	if m.cfg.Sessions == nil {
		return ""
	}
	return filepath.Dir(m.cfg.Sessions.PathForKey(key))
}

// ThreadStatus returns the status of a thread by ID.
func (m *Manager) ThreadStatus(id string) (tools.ThreadInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.threads {
		if t.id == id {
			return threadInfo(t), true
		}
	}
	return tools.ThreadInfo{}, false
}

// ContextBudget returns the effective context window and warn token for the
// thread identified by sessionKey. Returns (0, 0, false) if no thread exists.
func (m *Manager) ContextBudget(sessionKey string) (contextWindow int, warnToken int, ok bool) {
	m.mu.Lock()
	t, exists := m.threads[sessionKey]
	m.mu.Unlock()
	if !exists {
		return 0, 0, false
	}
	ct := t.contextBudget()
	return ct.ContextWindow, ct.WarnToken, true
}

// SystemPrompt builds the current system prompt for the thread identified by
// sessionKey. Returns ("", false) if no thread is loaded for that key.
func (m *Manager) SystemPrompt(sessionKey string) (string, bool) {
	m.mu.Lock()
	t, ok := m.threads[sessionKey]
	m.mu.Unlock()
	if !ok {
		return "", false
	}
	return t.buildSystemPrompt(), true
}

// ToolDefs returns the current tool definitions for the thread identified by
// sessionKey. Returns (nil, false) if no thread is loaded for that key.
func (m *Manager) ToolDefs(sessionKey string) ([]provider.ToolDef, bool) {
	m.mu.Lock()
	t, ok := m.threads[sessionKey]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}
	return t.tools.Defs(), true
}

// SessionStatus returns combined disk + in-memory state for a session key.
// Both fields are populated independently — a session may exist on disk with
// no thread loaded, or a thread may be active with no jsonl yet (rare).
func (m *Manager) SessionStatus(sessionKey string) tools.SessionStatusInfo {
	sessionKey = strings.TrimSpace(sessionKey)
	info := tools.SessionStatusInfo{SessionKey: sessionKey}
	if sessionKey == "" {
		return info
	}

	if m.cfg.Sessions != nil {
		dir := m.SessionDir(sessionKey)
		info.SessionDir = dir
		path := m.cfg.Sessions.PathForKey(sessionKey)
		if st, err := os.Stat(path); err == nil {
			info.Exists = true
			info.FileSizeBytes = st.Size()
			info.LastModified = st.ModTime()
			if s, readErr := session.ReadFile(path); readErr == nil {
				info.MessageCount = len(s.Messages)
			}
		}
		if dir != "" {
			info.Agent = session.MetaAgent(dir)
		}
	}

	m.mu.Lock()
	if t, ok := m.threads[sessionKey]; ok {
		info.ThreadActive = true
		ti := threadInfo(t)
		info.Thread = &ti
	}
	m.mu.Unlock()

	return info
}

// runningTurnThread returns the thread for key only if it is still running the
// same turn identified by turnStart (the ExecMetrics.TurnStart captured when a
// progress snapshot was taken). The progress scanner uses this to avoid
// delivering a note after the observed turn has ended (or a new turn started).
func (m *Manager) runningTurnThread(key string, turnStart time.Time) *Thread {
	m.mu.Lock()
	t := m.threads[key]
	m.mu.Unlock()
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.execMetrics == nil || !t.execMetrics.TurnStart.Equal(turnStart) {
		return nil
	}
	return t
}

// ListThreads returns a summary of all active threads.
func (m *Manager) ListThreads() []tools.ThreadInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := make([]tools.ThreadInfo, 0, len(m.threads))
	for _, t := range m.threads {
		list = append(list, threadInfo(t))
	}
	return list
}

func threadInfo(t *Thread) tools.ThreadInfo {
	info := tools.ThreadInfo{ID: t.id, SessionKey: t.sessionKey, LastUserActiveAt: t.lastUserActiveAt}
	switch t.state {
	case threadRunning:
		info.State = "running"
	default:
		if t.hasMessages() {
			info.State = "pending"
		} else {
			info.State = "idle"
		}
	}
	info.Pending = len(t.inbox) + len(t.pending)

	// Populate runtime metrics for running threads.
	t.mu.Lock()
	if t.execMetrics != nil {
		t.execMetrics.mu.Lock()
		info.Iterations = t.execMetrics.Iterations
		info.TotalToolCalls = t.execMetrics.TotalToolCalls
		info.CurrentTool = t.execMetrics.CurrentTool
		info.ElapsedSec = int(time.Since(t.execMetrics.TurnStart).Seconds())
		info.ToolTrace = append([]ToolCallRecord(nil), t.execMetrics.ToolCalls...)
		info.TurnStart = t.execMetrics.TurnStart
		info.LastTextDeltaAt = t.execMetrics.LastTextDeltaAt
		info.OriginRequest = t.execMetrics.OriginRequest
		t.execMetrics.mu.Unlock()
		info.TurnWakeSource = string(t.lastWakeSource)
	}
	t.mu.Unlock()

	return info
}

// persistAgent writes the agent name to {sessionDir}/meta.json so it
// survives thread GC and restarts. Only called for explicitly-specified agents.
func (m *Manager) persistAgent(sessionKey, agentName string) {
	dir := m.SessionDir(sessionKey)
	if dir == "" {
		return
	}
	session.UpdateMeta(dir, func(meta *session.Meta) {
		meta.Agent = agentName
	})
}
