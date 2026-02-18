// Package tools provides the tool interface and built-in tools.
package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"gopkg.in/yaml.v3"
)

const (
	toolResultMaxChars  = 20000
	toolLogMaxChars     = 50000
)

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
	tools   map[string]Tool
	logsDir string
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

// SetLogsDir sets the directory for tool call log files.
func (r *Registry) SetLogsDir(dir string) {
	r.logsDir = strings.TrimSpace(dir)
}

// Clone returns a shallow copy of the registry.
func (r *Registry) Clone() *Registry {
	cloned := NewRegistry()
	cloned.logsDir = r.logsDir
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
	latency := time.Since(start)
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
		"latencyMs", latency.Milliseconds(),
	)

	if r.logsDir != "" {
		go r.writeToolLog(name, args, result, start, latency, okResult)
	}

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
	r.Register(&HealthTool{Workspace: workspace})
	r.Register(&WebSearchTool{defaultMaxResults: cfg.WebSearchMaxResults})
	r.Register(&WebFetchTool{})
	r.Register(&ReadImageTool{workspace: workspace})
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

func (r *Registry) writeToolLog(name string, args json.RawMessage, result string, start time.Time, latency time.Duration, ok bool) {
	if err := os.MkdirAll(r.logsDir, 0755); err != nil {
		logger.Warn("failed to create tool logs dir", "dir", r.logsDir, "err", err)
		return
	}

	suffix := randomHex(3)
	fileName := fmt.Sprintf("%s-%s-%s.md", start.Format("2006-01-02-15-04-05"), name, suffix)

	status := "ok"
	if !ok {
		status = "error"
	}

	logResult := result
	if len(logResult) > toolLogMaxChars {
		logResult = logResult[:toolLogMaxChars] + "\n\n...(truncated)"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", name))
	sb.WriteString(fmt.Sprintf("- **Time**: %s\n", start.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("- **Latency**: %dms\n", latency.Milliseconds()))
	sb.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
	sb.WriteString("\n## Request\n\n")
	sb.WriteString(formatArgsReadable(args))
	sb.WriteString("\n## Response\n\n")
	sb.WriteString(logResult)
	sb.WriteByte('\n')

	if err := os.WriteFile(filepath.Join(r.logsDir, fileName), []byte(sb.String()), 0644); err != nil {
		logger.Warn("failed to write tool log", "file", fileName, "err", err)
	}
}

func formatArgsReadable(args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil || len(m) == 0 {
		return "(none)\n"
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return string(args) + "\n"
	}
	return "```yaml\n" + string(data) + "```\n"
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	}
	return hex.EncodeToString(buf)
}
