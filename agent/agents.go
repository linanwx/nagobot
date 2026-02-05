// Package agent provides agent definitions management.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// AgentDef represents an agent definition loaded from agents/ directory.
type AgentDef struct {
	Name    string // Filename without .md extension
	Path    string // Full path to the file
	Content string // Raw content of the file
}

// AgentRegistry manages agent definitions from the agents/ directory.
type AgentRegistry struct {
	agentsDir string
	agents    map[string]*AgentDef
}

// NewAgentRegistry creates a new agent registry from the workspace.
func NewAgentRegistry(workspace string) *AgentRegistry {
	r := &AgentRegistry{
		agentsDir: filepath.Join(workspace, "agents"),
		agents:    make(map[string]*AgentDef),
	}
	r.load()
	return r
}

// load reads all .md files from the agents directory.
func (r *AgentRegistry) load() {
	entries, err := os.ReadDir(r.agentsDir)
	if err != nil {
		logger.Debug("agents directory not found", "dir", r.agentsDir)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".md")
		path := filepath.Join(r.agentsDir, entry.Name())

		content, err := os.ReadFile(path)
		if err != nil {
			logger.Warn("failed to read agent file", "path", path, "err", err)
			continue
		}

		r.agents[name] = &AgentDef{
			Name:    name,
			Path:    path,
			Content: string(content),
		}
		logger.Debug("loaded agent definition", "name", name)
	}
}

// Get returns an agent definition by name.
func (r *AgentRegistry) Get(name string) (*AgentDef, bool) {
	def, ok := r.agents[name]
	return def, ok
}

// List returns all available agent names.
func (r *AgentRegistry) List() []string {
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	return names
}

// BuildPrompt builds a system prompt for the agent, replacing placeholders.
func (r *AgentRegistry) BuildPrompt(name string, workspace string, toolNames []string, task string) (string, error) {
	def, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("agent not found: %s (available: %v)", name, r.List())
	}

	prompt := def.Content

	// Replace standard placeholders
	prompt = strings.ReplaceAll(prompt, "{{TIME}}", time.Now().Format("2006-01-02 15:04 (Monday)"))
	prompt = strings.ReplaceAll(prompt, "{{WORKSPACE}}", workspace)
	prompt = strings.ReplaceAll(prompt, "{{TOOLS}}", strings.Join(toolNames, ", "))
	prompt = strings.ReplaceAll(prompt, "{{TASK}}", task)

	return prompt, nil
}

// Reload reloads all agent definitions from disk.
func (r *AgentRegistry) Reload() {
	r.agents = make(map[string]*AgentDef)
	r.load()
}
