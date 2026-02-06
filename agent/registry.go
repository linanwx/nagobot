package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/linanwx/nagobot/logger"
)

// AgentDef represents an agent template file under workspace/agents.
type AgentDef struct {
	Name    string // Callable name used by spawn_thread.agent
	Summary string // Short summary shown in system prompt context
	Path    string // Full path to the template file
}

// AgentRegistry loads agent templates from workspace/agents.
type AgentRegistry struct {
	workspace string
	agentsDir string
	agents    map[string]*AgentDef
	mu        sync.RWMutex
}

// NewRegistry creates a registry from workspace/agents and loads all templates.
func NewRegistry(workspace string) *AgentRegistry {
	r := &AgentRegistry{
		workspace: workspace,
		agentsDir: filepath.Join(workspace, "agents"),
		agents:    make(map[string]*AgentDef),
	}
	r.load()
	return r
}

func (r *AgentRegistry) load() {
	entries, err := os.ReadDir(r.agentsDir)
	if err != nil {
		logger.Debug("agents directory not found", "dir", r.agentsDir)
		return
	}

	next := make(map[string]*AgentDef)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(r.agentsDir, entry.Name())
		fileName := strings.TrimSuffix(entry.Name(), ".md")

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			logger.Warn("failed to read agent template", "path", path, "err", readErr)
			continue
		}

		meta, _, _, parseErr := parseTemplate(string(raw))
		if parseErr != nil {
			logger.Warn("invalid agent template front matter", "path", path, "err", parseErr)
		}

		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = fileName
		}

		summary := strings.TrimSpace(meta.Summary)
		if summary == "" {
			summary = strings.TrimSpace(meta.Description)
		}

		key := normalizeAgentName(name)
		if _, exists := next[key]; exists {
			logger.Warn("duplicate agent name, keeping first", "name", name, "path", path)
			continue
		}

		next[key] = &AgentDef{
			Name:    name,
			Summary: summary,
			Path:    path,
		}
	}

	r.mu.Lock()
	r.agents = next
	r.mu.Unlock()
}

// Get returns a prompt-builder agent by name, or nil if not found.
func (r *AgentRegistry) Get(name string) *Agent {
	key := normalizeAgentName(name)
	if key == "" {
		return nil
	}

	r.mu.RLock()
	def, ok := r.agents[key]
	r.mu.RUnlock()
	if !ok {
		return nil
	}

	return NewTemplateAgent(def.Name, def.Path, r.workspace)
}

// List returns all available agent names.
func (r *AgentRegistry) List() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.agents))
	for _, def := range r.agents {
		names = append(names, def.Name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

// BuildPromptSection renders a concise list of callable agents.
func (r *AgentRegistry) BuildPromptSection() string {
	r.mu.RLock()
	defs := make([]*AgentDef, 0, len(r.agents))
	for _, def := range r.agents {
		defs = append(defs, def)
	}
	r.mu.RUnlock()

	if len(defs) == 0 {
		return ""
	}

	sort.Slice(defs, func(i, j int) bool {
		return strings.ToLower(defs[i].Name) < strings.ToLower(defs[j].Name)
	})

	var sb strings.Builder
	sb.WriteString("Available agents (for spawn_thread.agent):\n")
	for _, def := range defs {
		if def.Summary != "" {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", def.Name, def.Summary))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s\n", def.Name))
	}
	return strings.TrimSpace(sb.String())
}

// Reload reloads agent templates from disk.
func (r *AgentRegistry) Reload() {
	r.load()
}

func normalizeAgentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
