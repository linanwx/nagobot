// Package agent implements the core agent loop and context management.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/bus"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/tools"
)

// pendingSubagentResult holds a subagent completion or error for injection into the next turn.
type pendingSubagentResult struct {
	TaskID string
	Result string
	Error  string
}

// Agent is the core agent that processes messages.
type Agent struct {
	id            string
	cfg           *config.Config
	provider      provider.Provider
	tools         *tools.Registry
	skills        *skills.Registry
	agents        *AgentRegistry // Agent definitions from agents/ directory
	bus           *bus.Bus
	subagents     *bus.SubagentManager
	workspace     string
	maxIterations int
	runner        *Runner // Reusable runner for agent loop
	toolsDefaults tools.DefaultToolsConfig
	sessions      *SessionManager

	// pendingResults keyed by session key to prevent cross-session leakage.
	// Empty key "" is used for stateless/CLI mode.
	pendingResults   map[string][]pendingSubagentResult
	pendingResultsMu sync.Mutex

	channelSender tools.ChannelSender // Set in serve mode for push delivery
}

// NewAgent creates a new agent.
func NewAgent(cfg *config.Config) (*Agent, error) {
	// Validate provider and model type
	if err := provider.ValidateProviderModelType(
		cfg.Agents.Defaults.Provider,
		cfg.Agents.Defaults.ModelType,
	); err != nil {
		return nil, err
	}

	// Get API key
	apiKey, err := cfg.GetAPIKey()
	if err != nil {
		return nil, err
	}
	apiBase := cfg.GetAPIBase()

	// Get workspace
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return nil, err
	}

	// Create provider
	var p provider.Provider
	modelType := cfg.Agents.Defaults.ModelType
	modelName := cfg.GetModelName()
	maxTokens := cfg.Agents.Defaults.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}
	temp := cfg.Agents.Defaults.Temperature
	if temp == 0 {
		temp = 0.95
	}

	switch cfg.Agents.Defaults.Provider {
	case "openrouter":
		p = provider.NewOpenRouterProvider(apiKey, apiBase, modelType, modelName, maxTokens, temp)
	case "anthropic":
		p = provider.NewAnthropicProvider(apiKey, apiBase, modelType, modelName, maxTokens, temp)
	default:
		return nil, errors.New("unknown provider: " + cfg.Agents.Defaults.Provider)
	}

	// Generate agent ID
	agentID := fmt.Sprintf("agent-%d", time.Now().UnixNano())

	// Create event bus
	eventBus := bus.NewBus(100)

	// Create tool registry
	toolRegistry := tools.NewRegistry()
	toolsDefaults := tools.DefaultToolsConfig{
		ExecTimeout:         cfg.Tools.Exec.Timeout,
		WebSearchMaxResults: cfg.Tools.Web.Search.MaxResults,
		RestrictToWorkspace: cfg.Tools.Exec.RestrictToWorkspace,
	}
	toolRegistry.RegisterDefaultTools(workspace, toolsDefaults)

	// Create skill registry
	skillRegistry := skills.NewRegistry()
	skillRegistry.RegisterBuiltinSkills()
	// Load custom skills from workspace
	skillsDir := filepath.Join(workspace, "skills")
	if err := skillRegistry.LoadFromDirectory(skillsDir); err != nil {
		logger.Warn("failed to load skills", "dir", skillsDir, "err", err)
	}

	// Create agent registry (loads from agents/ directory)
	agentRegistry := NewAgentRegistry(workspace)

	maxIter := cfg.Agents.Defaults.MaxToolIterations
	if maxIter == 0 {
		maxIter = 20
	}

	// Create the runner (used by both main agent and subagents)
	runner := NewRunner(p, toolRegistry, maxIter)

	// Create session manager (non-fatal if it fails)
	configDir, _ := config.ConfigDir()
	sessions, sessErr := NewSessionManager(configDir)
	if sessErr != nil {
		logger.Warn("session manager unavailable", "err", sessErr)
	}

	agent := &Agent{
		id:             agentID,
		cfg:            cfg,
		provider:       p,
		tools:          toolRegistry,
		skills:         skillRegistry,
		agents:         agentRegistry,
		bus:            eventBus,
		workspace:      workspace,
		maxIterations:  maxIter,
		runner:         runner,
		toolsDefaults:  toolsDefaults,
		sessions:       sessions,
		pendingResults: make(map[string][]pendingSubagentResult),
	}

	// Subscribe to subagent completion/error events.
	// If origin routing is available and sender is configured, push directly.
	// Otherwise, queue for injection into the next turn.
	handleSubagentResult := func(_ context.Context, event *bus.Event) {
		var data bus.SubagentEventData
		if err := event.ParseData(&data); err != nil {
			return
		}

		// Format the result message
		var text string
		if data.Error != "" {
			text = fmt.Sprintf("[Subagent %s failed]: %s", data.AgentID, data.Error)
		} else {
			text = fmt.Sprintf("[Subagent %s completed]:\n%s", data.AgentID, data.Result)
		}

		// Attempt push delivery if origin and sender are available
		if data.OriginChannel != "" && data.OriginReplyTo != "" && agent.channelSender != nil {
			if err := agent.channelSender.SendTo(context.Background(), data.OriginChannel, text, data.OriginReplyTo); err != nil {
				logger.Warn("subagent push delivery failed, queuing for next turn", "err", err)
			} else {
				return // Push succeeded, no need to queue
			}
		}

		// Fallback: queue for injection into next user message, keyed by session
		sessionKey := data.OriginSessionKey // Empty string for stateless/CLI
		agent.pendingResultsMu.Lock()
		agent.pendingResults[sessionKey] = append(agent.pendingResults[sessionKey], pendingSubagentResult{
			TaskID: data.AgentID,
			Result: data.Result,
			Error:  data.Error,
		})
		agent.pendingResultsMu.Unlock()
	}
	eventBus.Subscribe(bus.EventSubagentCompleted, handleSubagentResult)
	eventBus.Subscribe(bus.EventSubagentError, handleSubagentResult)

	// Create subagent manager with a runner that creates real subagent execution
	subagentMgr := bus.NewSubagentManager(eventBus, 5, agent.createSubagentRunner())
	agent.subagents = subagentMgr

	// Register subagent tools (now that subagentMgr is ready)
	toolRegistry.Register(tools.NewSpawnAgentTool(subagentMgr, agentID))
	toolRegistry.Register(tools.NewCheckAgentTool(subagentMgr))

	// Register skill tool for progressive loading
	toolRegistry.Register(tools.NewUseSkillTool(skillRegistry))

	return agent, nil
}

// SetChannelSender registers the send_message tool backed by the given sender,
// and enables push delivery for async subagent results.
func (a *Agent) SetChannelSender(sender tools.ChannelSender) {
	a.channelSender = sender
	a.tools.Register(tools.NewSendMessageTool(sender))
}

// Close cleans up agent resources.
func (a *Agent) Close() {
	if a.bus != nil {
		a.bus.Close()
	}
}

// ID returns the agent's ID.
func (a *Agent) ID() string {
	return a.id
}

// createSubagentRunner creates a runner function for subagents.
// This runner reuses the same provider but creates a separate tool registry
// (without spawn_agent to prevent recursive spawning).
func (a *Agent) createSubagentRunner() bus.SubagentRunner {
	return func(ctx context.Context, task *bus.SubagentTask) (string, error) {
		// task.Type contains the agent name (from agents/ directory)
		agentName := task.Type

		// Create a tool registry for the subagent (without spawn tools to prevent recursion)
		subTools := tools.NewRegistry()
		subTools.RegisterDefaultTools(a.workspace, a.toolsDefaults)
		// Note: we don't register spawn_agent and check_agent to prevent infinite recursion

		// Create a runner for this subagent (same max iterations as main agent)
		subRunner := NewRunner(a.provider, subTools, a.maxIterations)

		// Build subagent system prompt from agents/ directory
		systemPrompt, err := a.agents.BuildPrompt(agentName, a.workspace, subTools.Names(), task.Task)
		if err != nil {
			return "", err
		}

		// Build the user message (task + optional context)
		userMessage := task.Task
		if task.Context != "" {
			userMessage = fmt.Sprintf("%s\n\nContext:\n%s", task.Task, task.Context)
		}

		// Execute using the runner
		return subRunner.Run(ctx, systemPrompt, userMessage)
	}
}

// drainPendingResults collects and clears pending subagent results for a specific session.
// Returns a formatted string to prepend to the user message, or empty if none.
func (a *Agent) drainPendingResults(sessionKey string) string {
	a.pendingResultsMu.Lock()
	results := a.pendingResults[sessionKey]
	if len(results) > 0 {
		delete(a.pendingResults, sessionKey)
	}
	a.pendingResultsMu.Unlock()

	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Async subagent results since last turn]\n")
	for _, r := range results {
		if r.Error != "" {
			sb.WriteString(fmt.Sprintf("- %s failed: %s\n", r.TaskID, r.Error))
		} else {
			sb.WriteString(fmt.Sprintf("- %s completed: %s\n", r.TaskID, r.Result))
		}
	}
	return sb.String()
}

// Run processes a user message and returns the assistant's response (stateless).
func (a *Agent) Run(ctx context.Context, userMessage string) (string, error) {
	// Prepend any pending subagent results (stateless = empty session key)
	if prefix := a.drainPendingResults(""); prefix != "" {
		userMessage = prefix + "---\nUser message: " + userMessage
	}

	// Build system prompt
	systemPrompt := a.buildSystemPrompt()

	// Use the runner to execute the agent loop
	return a.runner.Run(ctx, systemPrompt, userMessage)
}

// RunInSession processes a user message within a named session, replaying history.
// Falls back to stateless Run() if session manager is unavailable.
func (a *Agent) RunInSession(ctx context.Context, sessionKey string, userMessage string) (string, error) {
	// Prepend any pending subagent results for this specific session
	if prefix := a.drainPendingResults(sessionKey); prefix != "" {
		userMessage = prefix + "---\nUser message: " + userMessage
	}

	if a.sessions == nil {
		return a.Run(ctx, userMessage)
	}

	session, err := a.sessions.Get(sessionKey)
	if err != nil {
		logger.Warn("failed to load session, falling back to stateless", "key", sessionKey, "err", err)
		return a.Run(ctx, userMessage)
	}

	// Build messages: system prompt + session history + new user message
	systemPrompt := a.buildSystemPrompt()
	messages := make([]provider.Message, 0, len(session.Messages)+2)
	messages = append(messages, provider.SystemMessage(systemPrompt))
	messages = append(messages, session.Messages...)
	messages = append(messages, provider.UserMessage(userMessage))

	response, err := a.runner.RunWithMessages(ctx, messages)
	if err != nil {
		return "", err
	}

	// Persist the exchange
	session.Messages = append(session.Messages, provider.UserMessage(userMessage))
	session.Messages = append(session.Messages, provider.AssistantMessage(response))

	// Cap history to prevent token overflow (keep last 40 messages = 20 exchanges)
	const maxSessionMessages = 40
	if len(session.Messages) > maxSessionMessages {
		session.Messages = session.Messages[len(session.Messages)-maxSessionMessages:]
	}

	if saveErr := a.sessions.Save(session); saveErr != nil {
		logger.Warn("failed to save session", "key", sessionKey, "err", saveErr)
	}

	return response, nil
}

// buildSystemPrompt builds the system prompt from SOUL.md in workspace.
// SOUL.md should contain the complete system prompt template with placeholders.
func (a *Agent) buildSystemPrompt() string {
	soulPath := filepath.Join(a.workspace, "SOUL.md")
	content, err := os.ReadFile(soulPath)
	if err != nil {
		// Fallback to minimal prompt if SOUL.md doesn't exist
		logger.Warn("SOUL.md not found, using minimal prompt", "path", soulPath)
		return fmt.Sprintf(`You are nagobot, a helpful AI assistant.

Current Time: %s
Workspace: %s
Available Tools: %s
`, time.Now().Format("2006-01-02 15:04 (Monday)"), a.workspace, strings.Join(a.tools.Names(), ", "))
	}

	// Replace placeholders in SOUL.md
	prompt := string(content)
	prompt = strings.ReplaceAll(prompt, "{{TIME}}", time.Now().Format("2006-01-02 15:04 (Monday)"))
	prompt = strings.ReplaceAll(prompt, "{{WORKSPACE}}", a.workspace)
	prompt = strings.ReplaceAll(prompt, "{{TOOLS}}", strings.Join(a.tools.Names(), ", "))

	// Identity / User / Agents context files (optional, empty if not present)
	identityContent, _ := os.ReadFile(filepath.Join(a.workspace, "IDENTITY.md"))
	prompt = strings.ReplaceAll(prompt, "{{IDENTITY}}", strings.TrimSpace(string(identityContent)))

	userContent, _ := os.ReadFile(filepath.Join(a.workspace, "USER.md"))
	prompt = strings.ReplaceAll(prompt, "{{USER}}", strings.TrimSpace(string(userContent)))

	agentsContent, _ := os.ReadFile(filepath.Join(a.workspace, "AGENTS.md"))
	prompt = strings.ReplaceAll(prompt, "{{AGENTS}}", strings.TrimSpace(string(agentsContent)))

	// Skills section
	skillsPrompt := a.skills.BuildPromptSection()
	prompt = strings.ReplaceAll(prompt, "{{SKILLS}}", skillsPrompt)

	// Memory (optional)
	memoryPath := filepath.Join(a.workspace, "memory", "MEMORY.md")
	memoryContent, _ := os.ReadFile(memoryPath)
	prompt = strings.ReplaceAll(prompt, "{{MEMORY}}", strings.TrimSpace(string(memoryContent)))

	// Today's notes (optional)
	todayFile := time.Now().Format("2006-01-02") + ".md"
	todayPath := filepath.Join(a.workspace, "memory", todayFile)
	todayContent, _ := os.ReadFile(todayPath)
	prompt = strings.ReplaceAll(prompt, "{{TODAY}}", strings.TrimSpace(string(todayContent)))

	return prompt
}

// ============================================================================
// Session management (simple in-memory + file persistence)
// ============================================================================

// Session represents a conversation session.
type Session struct {
	Key       string             `json:"key"`
	Messages  []provider.Message `json:"messages"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// SessionManager manages conversation sessions.
type SessionManager struct {
	sessionsDir string
	cache       map[string]*Session
}

// NewSessionManager creates a new session manager.
func NewSessionManager(configDir string) (*SessionManager, error) {
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, err
	}
	return &SessionManager{
		sessionsDir: sessionsDir,
		cache:       make(map[string]*Session),
	}, nil
}

// Get returns a session by key, creating one if it doesn't exist.
func (m *SessionManager) Get(key string) (*Session, error) {
	// Check cache
	if s, ok := m.cache[key]; ok {
		return s, nil
	}

	// Try to load from disk
	path := m.sessionPath(key)
	data, err := os.ReadFile(path)
	if err == nil {
		var s Session
		if err := json.Unmarshal(data, &s); err == nil {
			m.cache[key] = &s
			return &s, nil
		}
	}

	// Create new session
	s := &Session{
		Key:       key,
		Messages:  []provider.Message{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.cache[key] = s
	return s, nil
}

// Save saves a session to disk.
func (m *SessionManager) Save(s *Session) error {
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.sessionPath(s.Key), data, 0644)
}

// sessionPath returns the file path for a session.
func (m *SessionManager) sessionPath(key string) string {
	// Sanitize key for filename
	safe := strings.ReplaceAll(key, "/", "_")
	safe = strings.ReplaceAll(safe, ":", "_")
	return filepath.Join(m.sessionsDir, safe+".json")
}

// ============================================================================
// Memory management
// ============================================================================

// Memory manages long-term and daily memory.
type Memory struct {
	memoryDir string
}

// NewMemory creates a new memory manager.
func NewMemory(workspace string) (*Memory, error) {
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return nil, err
	}
	return &Memory{memoryDir: memoryDir}, nil
}

// ReadLongTerm reads the long-term memory.
func (m *Memory) ReadLongTerm() (string, error) {
	path := filepath.Join(m.memoryDir, "MEMORY.md")
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(content), nil
}

// WriteLongTerm writes to the long-term memory.
func (m *Memory) WriteLongTerm(content string) error {
	path := filepath.Join(m.memoryDir, "MEMORY.md")
	return os.WriteFile(path, []byte(content), 0644)
}

// ReadToday reads today's notes.
func (m *Memory) ReadToday() (string, error) {
	path := filepath.Join(m.memoryDir, time.Now().Format("2006-01-02")+".md")
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(content), nil
}

// AppendToday appends to today's notes.
func (m *Memory) AppendToday(content string) error {
	path := filepath.Join(m.memoryDir, time.Now().Format("2006-01-02")+".md")

	// Read existing content
	existing, _ := os.ReadFile(path)

	// If file doesn't exist, add header
	if len(existing) == 0 {
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		existing = []byte(header)
	}

	// Append new content
	newContent := string(existing) + content + "\n"
	return os.WriteFile(path, []byte(newContent), 0644)
}
