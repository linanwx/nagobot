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
	Name             string // Callable name used by dispatch(to=subagent|fork).agent
	Description      string // Short description shown in system prompt context
	Specialty        string // Agent specialty declared in frontmatter (e.g. "chat", "toolcall")
	Provider         string // Provider name declared in frontmatter (optional, used for model-pinned agents)
	Path             string // Full path to the template file
	ContextWindowCap int    // Parsed token cap; 0 = no cap
	TierLossyMode    string // "slide_window" | "" (disabled)
	TierLossyKeep    int    // slide_window: last N turns to retain
}

const agentsBuiltinDir = "agents-builtin"

// AgentRegistry loads agent templates from workspace/agents and workspace/agents-builtin.
type AgentRegistry struct {
	workspace    string
	agentsDirs   []string // scanned in order; later dirs override earlier on name conflict
	agents       map[string]*AgentDef
	lastSnapshot dirSnapshot // cached file modtimes for change detection
	mu           sync.RWMutex
}

// dirSnapshot records file modtimes for change detection.
type dirSnapshot struct {
	files map[string]int64 // path → modtime (UnixNano)
}

func takeDirSnapshot(dirs []string) dirSnapshot {
	snap := dirSnapshot{files: make(map[string]int64)}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if info, err := e.Info(); err == nil {
				snap.files[filepath.Join(dir, e.Name())] = info.ModTime().UnixNano()
			}
		}
	}
	return snap
}

func (s dirSnapshot) equals(other dirSnapshot) bool {
	if len(s.files) != len(other.files) {
		return false
	}
	for k, v := range s.files {
		if ov, ok := other.files[k]; !ok || v != ov {
			return false
		}
	}
	return true
}

// NewRegistry creates a registry and loads all templates.
// Scans agents/ (user) first, then agents-builtin/ (overrides stale user copies).
func NewRegistry(workspace string) *AgentRegistry {
	r := &AgentRegistry{
		workspace: workspace,
		agentsDirs: []string{
			filepath.Join(workspace, "agents"),         // user
			filepath.Join(workspace, agentsBuiltinDir), // builtin (overrides)
		},
		agents: make(map[string]*AgentDef),
	}
	r.load()
	return r
}

func (r *AgentRegistry) load() {
	snap := takeDirSnapshot(r.agentsDirs)
	r.mu.RLock()
	same := snap.equals(r.lastSnapshot)
	r.mu.RUnlock()
	if same {
		return
	}

	next := make(map[string]*AgentDef)
	for _, dir := range r.agentsDirs {
		loadAgentsFromDir(dir, next)
	}
	r.mu.Lock()
	r.agents = next
	r.lastSnapshot = snap
	r.mu.Unlock()
}

func loadAgentsFromDir(dir string, dest map[string]*AgentDef) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Debug("agents directory not found", "dir", dir)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
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

		capTokens := ParseTokenAmount(meta.ContextWindowCap)
		if strings.TrimSpace(meta.ContextWindowCap) != "" && capTokens <= 0 {
			logger.Warn("invalid context_window_cap, ignoring", "path", path, "value", meta.ContextWindowCap)
		}

		tierLossyMode := strings.TrimSpace(meta.TierLossyMode)
		tierLossyKeep := meta.TierLossyKeep
		if tierLossyMode != "" {
			if tierLossyMode != "slide_window" {
				logger.Warn("invalid tier_lossy_mode, ignoring", "path", path, "value", tierLossyMode)
				tierLossyMode = ""
				tierLossyKeep = 0
			} else if tierLossyKeep <= 0 {
				logger.Warn("tier_lossy_mode requires positive tier_lossy_keep, ignoring", "path", path, "mode", tierLossyMode, "keep", tierLossyKeep)
				tierLossyMode = ""
				tierLossyKeep = 0
			}
		}

		dest[normalizeAgentName(name)] = &AgentDef{
			Name:             name,
			Description:      strings.TrimSpace(meta.Description),
			Specialty:        strings.TrimSpace(meta.Specialty),
			Provider:         strings.TrimSpace(meta.Provider),
			Path:             path,
			ContextWindowCap: capTokens,
			TierLossyMode:    tierLossyMode,
			TierLossyKeep:    tierLossyKeep,
		}
	}
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
		if strings.HasPrefix(strings.ToLower(def.Name), "fixed-to") {
			continue
		}
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
	sb.WriteString("Available agents (use with dispatch to=subagent|fork, agent=<name>):\n")
	for _, def := range defs {
		if def.Description != "" {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", def.Name, def.Description))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s\n", def.Name))
	}
	return strings.TrimSpace(sb.String())
}

// ClampContextWindow applies the agent's ContextWindowCap to base.
// Nil-safe; returns base unchanged when no cap is set or base is already smaller.
// A base of 0 (unknown) is replaced by the cap.
func (d *AgentDef) ClampContextWindow(base int) int {
	if d == nil || d.ContextWindowCap <= 0 {
		return base
	}
	if base == 0 || d.ContextWindowCap < base {
		return d.ContextWindowCap
	}
	return base
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
