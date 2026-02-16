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
	Name        string // Callable name used by spawn_thread.agent
	Description string // Short description shown in system prompt context
	Model       string // Model type declared in frontmatter (e.g. "chat", "search")
	Path        string // Full path to the template file
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

		meta, _, _, parseErr := ParseTemplate(string(raw))
		if parseErr != nil {
			logger.Warn("invalid agent template front matter", "path", path, "err", parseErr)
		}

		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = fileName
		}

		key := normalizeAgentName(name)
		if _, exists := next[key]; exists {
			logger.Warn("duplicate agent name, keeping first", "name", name, "path", path)
			continue
		}

		next[key] = &AgentDef{
			Name:        name,
			Description: strings.TrimSpace(meta.Description),
			Model:       strings.TrimSpace(meta.Model),
			Path:        path,
		}
	}

	r.mu.Lock()
	r.agents = next
	r.mu.Unlock()
}

// New creates an agent by name. Defaults to "soul" if name is empty.
// Reloads templates from disk before resolving. Returns an error if an
// explicit name is provided but not found in the registry.
func (r *AgentRegistry) New(name string) (*Agent, error) {
	explicit := strings.TrimSpace(name)
	if explicit == "" {
		explicit = "soul"
	}

	if r == nil {
		return newAgent(explicit, ""), nil
	}

	r.load()

	r.mu.RLock()
	_, found := r.agents[normalizeAgentName(explicit)]
	r.mu.RUnlock()

	if !found && strings.TrimSpace(name) != "" {
		return nil, fmt.Errorf("agent %q not found", explicit)
	}

	return newAgent(explicit, r.workspace), nil
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
		if def.Description != "" {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", def.Name, def.Description))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s\n", def.Name))
	}
	return strings.TrimSpace(sb.String())
}

// Def returns the AgentDef for the given name, or nil if not found.
func (r *AgentRegistry) Def(name string) *AgentDef {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[normalizeAgentName(name)]
}

func normalizeAgentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
