package thread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/internal/runtimecfg"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
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
	DefaultProvider provider.Provider
	ProviderFactory ProviderFactory
	Tools           *tools.Registry
	Skills          *skills.Registry
	Agents          *agent.AgentRegistry
	Workspace       string
	MaxIterations   int
	Sessions        *SessionManager
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
func (m *Manager) GetOrCreate(sessionKey string, ag *agent.Agent, sink Sink) *Thread {
	if strings.TrimSpace(sessionKey) == "" {
		return NewPlain(m.cfg, ag, sink).Thread
	}
	return m.GetOrCreateChannel(sessionKey, ag, sink).Thread
}

// GetOrCreateChannel returns an existing channel thread, or creates one.
func (m *Manager) GetOrCreateChannel(sessionKey string, ag *agent.Agent, sink Sink) *ChannelThread {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "channel:default"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.threads[sessionKey]; ok {
		if ag != nil {
			t.agent = ag
		}
		t.sink = sink
		return t
	}

	t := NewChannel(m.cfg, ag, sessionKey, sink)
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
	maxIter   int

	sessionKey string
	sink       Sink
	allowSpawn bool

	mu             sync.Mutex
	children       map[string]*childState
	childCounter   int64
	pendingResults []pendingChildResult
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

type pendingChildResult struct {
	ID     string
	Result string
	Err    error
}

// NewPlain creates a stateless plain thread.
func NewPlain(cfg *Config, ag *agent.Agent, sink Sink) *PlainThread {
	return &PlainThread{
		Thread: newThread(cfg, ag, "", sink, ThreadTypePlain, true),
	}
}

// NewChannel creates a channel-bound thread that can persist session state.
func NewChannel(cfg *Config, ag *agent.Agent, sessionKey string, sink Sink) *ChannelThread {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "channel:default"
	}
	return &ChannelThread{
		PlainThread: &PlainThread{
			Thread: newThread(cfg, ag, sessionKey, sink, ThreadTypeChannel, true),
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
			Thread: newThread(cfg, ag, sessionKey, sink, ThreadTypeChild, false),
		},
	}
}

// New keeps backward compatibility while preferring explicit constructors.
func New(cfg *Config, ag *agent.Agent, sessionKey string, sink Sink) *Thread {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return NewPlain(cfg, ag, sink).Thread
	}
	return NewChannel(cfg, ag, sessionKey, sink).Thread
}

func newThread(cfg *Config, ag *agent.Agent, sessionKey string, sink Sink, kind ThreadType, allowSpawn bool) *Thread {
	if cfg == nil {
		cfg = &Config{}
	}
	if ag == nil {
		ag = agent.NewRawAgent("default", "You are a helpful AI assistant.")
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = runtimecfg.AgentDefaultMaxToolIterations
	}

	agentRegistry := cfg.Agents
	if agentRegistry == nil && cfg.Workspace != "" {
		agentRegistry = agent.NewRegistry(cfg.Workspace)
	}

	return &Thread{
		id:         fmt.Sprintf("thread-%d", time.Now().UnixNano()),
		kind:       kind,
		cfg:        cfg,
		agent:      ag,
		provider:   cfg.DefaultProvider,
		tools:      cfg.Tools,
		skills:     cfg.Skills,
		agents:     agentRegistry,
		workspace:  cfg.Workspace,
		maxIter:    maxIter,
		sessionKey: sessionKey,
		sink:       sink,
		allowSpawn: allowSpawn,
		children:   make(map[string]*childState),
	}
}

// Type returns the runtime thread type.
func (t *Thread) Type() ThreadType {
	return t.kind
}

// Run executes one thread turn.
func (t *Thread) Run(ctx context.Context, userMessage string) (string, error) {
	prov, err := t.resolveProvider()
	if err != nil {
		return "", err
	}

	runtimeTools := t.runtimeTools()
	skillsSection := t.buildSkillsSection()

	if prefix := t.drainPendingResults(); prefix != "" {
		userMessage = prefix + "---\nUser message: " + userMessage
	}

	promptCtx := agent.PromptContext{
		Workspace: t.workspace,
		Time:      time.Now(),
		ToolNames: runtimeTools.Names(),
		Skills:    skillsSection,
	}

	systemPrompt := ""
	if t.agent != nil && t.agent.BuildPrompt != nil {
		systemPrompt = t.agent.BuildPrompt(promptCtx)
	}
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = agent.NewRawAgent("fallback", "You are a helpful AI assistant.").BuildPrompt(promptCtx)
	}

	messages := make([]provider.Message, 0, 2)
	messages = append(messages, provider.SystemMessage(systemPrompt))

	session := t.loadSession()
	if session != nil {
		messages = append(messages, session.Messages...)
	}

	messages = append(messages, provider.UserMessage(userMessage))

	runner := NewRunner(prov, runtimeTools, t.maxIter)
	response, err := runner.RunWithMessages(ctx, messages)
	if err != nil {
		return "", err
	}

	if session != nil {
		session.Messages = append(session.Messages, provider.UserMessage(userMessage))
		session.Messages = append(session.Messages, provider.AssistantMessage(response))

		if len(session.Messages) > runtimecfg.AgentSessionMaxMessages {
			session.Messages = session.Messages[len(session.Messages)-runtimecfg.AgentSessionMaxMessages:]
		}

		if saveErr := t.cfg.Sessions.Save(session); saveErr != nil {
			logger.Warn("failed to save session", "key", t.sessionKey, "err", saveErr)
		}
	}

	if t.sink != nil {
		if err := t.sink(ctx, response); err != nil {
			return "", err
		}
	}

	return response, nil
}

// SpawnChild spawns a child thread for delegated work.
func (t *Thread) SpawnChild(ctx context.Context, ag *agent.Agent, task, taskContext string, wait bool) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("task is required")
	}

	if ag == nil {
		ag = t.agent
	}
	if ag == nil {
		return "", fmt.Errorf("child agent is not configured")
	}

	childAgent := wrapAgentTaskPlaceholder(ag, task)
	childSessionKey := ""
	if t.cfg != nil && t.cfg.Sessions != nil {
		parentIdentity := strings.TrimSpace(t.sessionKey)
		if parentIdentity == "" {
			parentIdentity = strings.TrimSpace(t.id)
		}
		childSessionKey = generateChildSessionKey(parentIdentity)
	}
	child := newChildWithSession(t.cfg, childAgent, nil, childSessionKey)

	userMessage := task
	if strings.TrimSpace(taskContext) != "" {
		userMessage = fmt.Sprintf("%s\n\nContext:\n%s", task, taskContext)
	}

	if wait {
		return child.Run(ctx, userMessage)
	}

	t.mu.Lock()
	t.childCounter++
	childID := fmt.Sprintf("%s-child-%d", t.id, t.childCounter)
	state := &childState{
		done: make(chan struct{}),
	}
	t.children[childID] = state
	t.mu.Unlock()

	go func() {
		result, err := child.Run(ctx, userMessage)
		t.mu.Lock()
		state.result = result
		state.err = err
		close(state.done)
		t.pendingResults = append(t.pendingResults, pendingChildResult{ID: childID, Result: result, Err: err})
		t.mu.Unlock()
	}()

	return childID, nil
}

// GetChild returns child thread status and result.
func (t *Thread) GetChild(childID string) (status, result string, err error) {
	t.mu.Lock()
	state, ok := t.children[childID]
	if !ok {
		t.mu.Unlock()
		return "", "", fmt.Errorf("child thread not found: %s", childID)
	}

	select {
	case <-state.done:
		result = state.result
		err = state.err
		t.mu.Unlock()
		if err != nil {
			return "failed", "", err
		}
		return "completed", result, nil
	default:
		t.mu.Unlock()
		return "running", "", nil
	}
}

func (t *Thread) resolveProvider() (provider.Provider, error) {
	if t.agent != nil && (strings.TrimSpace(t.agent.ProviderName) != "" || strings.TrimSpace(t.agent.ModelType) != "") {
		if t.cfg.ProviderFactory == nil {
			return nil, fmt.Errorf("provider override requested but provider factory is not configured")
		}
		return t.cfg.ProviderFactory(t.agent.ProviderName, t.agent.ModelType)
	}

	if t.provider == nil {
		if t.cfg.ProviderFactory == nil {
			return nil, fmt.Errorf("default provider is not configured")
		}
		return t.cfg.ProviderFactory("", "")
	}

	return t.provider, nil
}

func (t *Thread) runtimeTools() *tools.Registry {
	runtimeTools := tools.NewRegistry()
	if t.tools != nil {
		runtimeTools = t.tools.Clone()
	}

	if t.allowSpawn {
		runtimeTools.Register(tools.NewSpawnThreadTool(t, t.agents))
		runtimeTools.Register(tools.NewCheckThreadTool(t))
	}

	return runtimeTools
}

func (t *Thread) loadSession() *Session {
	if t.cfg == nil || strings.TrimSpace(t.sessionKey) == "" || t.cfg.Sessions == nil {
		return nil
	}

	session, err := t.cfg.Sessions.Get(t.sessionKey)
	if err != nil {
		logger.Warn("failed to load session", "key", t.sessionKey, "err", err)
		return nil
	}
	return session
}

func generateChildSessionKey(parentIdentity string) string {
	parentIdentity = strings.TrimSpace(parentIdentity)
	if parentIdentity == "" {
		parentIdentity = "root"
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("child:%s:%d", parentIdentity, time.Now().UnixNano())
	}
	return fmt.Sprintf("child:%s:%s", parentIdentity, hex.EncodeToString(buf))
}

func (t *Thread) buildSkillsSection() string {
	if t.skills == nil || strings.TrimSpace(t.workspace) == "" {
		return ""
	}

	skillsDir := filepath.Join(t.workspace, runtimecfg.WorkspaceSkillsDirName)
	if err := t.skills.ReloadFromDirectory(skillsDir); err != nil {
		logger.Warn("failed to reload skills", "dir", skillsDir, "err", err)
	}
	return t.skills.BuildPromptSection()
}

func (t *Thread) drainPendingResults() string {
	t.mu.Lock()
	pending := t.pendingResults
	t.pendingResults = nil
	t.mu.Unlock()

	if len(pending) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Async thread results since last turn]\n")
	for _, r := range pending {
		if r.Err != nil {
			sb.WriteString(fmt.Sprintf("- %s failed: %v\n", r.ID, r.Err))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s completed: %s\n", r.ID, r.Result))
	}
	return sb.String()
}

func wrapAgentTaskPlaceholder(base *agent.Agent, task string) *agent.Agent {
	if base == nil {
		return nil
	}
	return &agent.Agent{
		Name:         base.Name,
		ProviderName: base.ProviderName,
		ModelType:    base.ModelType,
		BuildPrompt: func(ctx agent.PromptContext) string {
			if base.BuildPrompt == nil {
				return ""
			}
			prompt := base.BuildPrompt(ctx)
			return strings.ReplaceAll(prompt, "{{TASK}}", task)
		},
	}
}
