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
	"time"

	"github.com/linanwx/nagobot/bus"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/tools"
)

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

	agent := &Agent{
		id:            agentID,
		cfg:           cfg,
		provider:      p,
		tools:         toolRegistry,
		skills:        skillRegistry,
		agents:        agentRegistry,
		bus:           eventBus,
		workspace:     workspace,
		maxIterations: maxIter,
		runner:        runner,
		toolsDefaults: toolsDefaults,
	}

	// Create subagent manager with a runner that creates real subagent execution
	subagentMgr := bus.NewSubagentManager(eventBus, 5, agent.createSubagentRunner())
	agent.subagents = subagentMgr

	// Register subagent tools (now that subagentMgr is ready)
	toolRegistry.Register(tools.NewSpawnAgentTool(subagentMgr, agentID))
	toolRegistry.Register(tools.NewCheckAgentTool(subagentMgr))

	// Publish agent started event
	eventBus.PublishAgentStarted(agentID)

	return agent, nil
}

// Close cleans up agent resources.
func (a *Agent) Close() {
	if a.bus != nil {
		a.bus.PublishAgentStopped(a.id)
		a.bus.Close()
	}
}

// ID returns the agent's ID.
func (a *Agent) ID() string {
	return a.id
}

// Bus returns the agent's event bus.
func (a *Agent) Bus() *bus.Bus {
	return a.bus
}

// createSubagentRunner creates a runner function for subagents.
// This runner reuses the same provider but creates a separate tool registry
// (without spawn_agent to prevent recursive spawning).
func (a *Agent) createSubagentRunner() bus.SubagentRunner {
	return func(ctx context.Context, task *bus.SubagentTask) (string, error) {
		// task.Type contains the agent name (from agents/ directory)
		agentName := task.Type
		if agentName == "" {
			return "", fmt.Errorf("agent name is required (available: %v)", a.agents.List())
		}

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

// Run processes a user message and returns the assistant's response.
func (a *Agent) Run(ctx context.Context, userMessage string) (string, error) {
	// Build system prompt
	systemPrompt := a.buildSystemPrompt()

	// Use the runner to execute the agent loop
	return a.runner.Run(ctx, systemPrompt, userMessage)
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
