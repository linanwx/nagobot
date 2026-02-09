// Package tools provides the tool interface and built-in tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

const toolResultMaxChars = 100000

// Tool is the interface for agent tools.
type Tool interface {
	// Def returns the tool definition for the LLM.
	Def() provider.ToolDef
	// Run executes the tool with the given arguments and returns the result.
	// Errors are returned as strings (for the LLM to interpret).
	Run(ctx context.Context, args json.RawMessage) string
}

func parseArgs[T any](args json.RawMessage, target *T) string {
	if err := json.Unmarshal(args, target); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}
	return ""
}

// Registry holds registered tools.
type Registry struct {
	tools map[string]Tool
}

// DefaultToolsConfig provides defaults for built-in tools.
type DefaultToolsConfig struct {
	ExecTimeout         int
	WebSearchMaxResults int
	RestrictToWorkspace bool
	Skills              SkillProvider
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Clone returns a shallow copy of the registry.
func (r *Registry) Clone() *Registry {
	cloned := NewRegistry()
	for name, tool := range r.tools {
		cloned.tools[name] = tool
	}
	return cloned
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Def().Function.Name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Defs returns all tool definitions.
func (r *Registry) Defs() []provider.ToolDef {
	defs := make([]provider.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Def())
	}
	return defs
}

// Run executes a tool by name.
func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) string {
	start := time.Now()
	logger.Debug("tool call", "tool", name, "args", string(args))

	t, ok := r.tools[name]
	if !ok {
		logger.Error("tool not found", "tool", name)
		logger.Debug("tool call finished", "tool", name, "ok", false, "latencyMs", time.Since(start).Milliseconds())
		return fmt.Sprintf("Error: unknown tool '%s'", name)
	}

	result := t.Run(ctx, args)
	originalChars := len(result)
	result, truncated := truncateWithNotice(result, toolResultMaxChars)
	okResult := !strings.HasPrefix(result, "Error:")
	logger.Debug(
		"tool call finished",
		"tool", name,
		"ok", okResult,
		"truncated", truncated,
		"resultChars", len(result),
		"originalChars", originalChars,
		"latencyMs", time.Since(start).Milliseconds(),
	)
	return result
}

// Names returns the names of all registered tools.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RegisterDefaultTools registers the default file tools.
func (r *Registry) RegisterDefaultTools(workspace string, cfg DefaultToolsConfig) {
	r.Register(&ReadFileTool{workspace: workspace})
	r.Register(&WriteFileTool{workspace: workspace})
	r.Register(&AppendFileTool{workspace: workspace})
	r.Register(&EditFileTool{workspace: workspace})
	r.Register(&ExecTool{workspace: workspace, defaultTimeout: cfg.ExecTimeout, restrictToWorkspace: cfg.RestrictToWorkspace})
	r.Register(NewHealthTool(workspace, "", "", nil))
	r.Register(&ScriptEvalTool{workspace: workspace})
	r.Register(&WebSearchTool{defaultMaxResults: cfg.WebSearchMaxResults})
	r.Register(&WebFetchTool{})
	if cfg.Skills != nil {
		r.Register(NewUseSkillTool(cfg.Skills))
	}
}

// expandPath expands ~ to home directory and resolves the path.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	}
	return path
}

// resolveToolPath resolves relative file tool paths from workspace.
func resolveToolPath(path, workspace string) string {
	path = expandPath(path)
	if path == "" || filepath.IsAbs(path) || workspace == "" {
		return path
	}
	return filepath.Join(workspace, path)
}
