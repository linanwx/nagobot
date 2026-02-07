package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	healthsnap "github.com/linanwx/nagobot/internal/health"
	"github.com/linanwx/nagobot/internal/runtimecfg"
	"github.com/linanwx/nagobot/provider"
)

// HealthRuntimeContext is thread/session metadata injected at runtime.
type HealthRuntimeContext struct {
	ThreadID    string
	ThreadType  string
	SessionKey  string
	SessionFile string
}

// HealthContextProvider returns dynamic runtime context.
type HealthContextProvider func() HealthRuntimeContext

// HealthTool reports runtime health info for the current process.
type HealthTool struct {
	workspace string
	ctxFn     HealthContextProvider
}

// NewHealthTool creates a health tool with optional workspace and runtime context provider.
func NewHealthTool(workspace string, ctxFn HealthContextProvider) *HealthTool {
	return &HealthTool{
		workspace: strings.TrimSpace(workspace),
		ctxFn:     ctxFn,
	}
}

// Def returns the tool definition.
func (t *HealthTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "health",
			Description: "Get runtime health information for this nagobot process, including paths, thread/session diagnostics, and optional workspace tree snapshot.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"format": map[string]any{
						"type":        "string",
						"description": "Optional output format: 'json' or 'text'. Defaults to 'json'.",
						"enum":        []string{"json", "text"},
					},
					"include_tree": map[string]any{
						"type":        "boolean",
						"description": "When true, include a bounded workspace path tree.",
					},
					"tree_depth": map[string]any{
						"type":        "integer",
						"description": "Optional max depth for workspace tree (default: 2).",
					},
					"tree_max_entries": map[string]any{
						"type":        "integer",
						"description": "Optional max number of tree entries to return (default: 200).",
					},
				},
			},
		},
	}
}

type healthArgs struct {
	Format         string `json:"format,omitempty"`
	IncludeTree    bool   `json:"include_tree,omitempty"`
	TreeDepth      int    `json:"tree_depth,omitempty"`
	TreeMaxEntries int    `json:"tree_max_entries,omitempty"`
}

// Run executes the tool.
func (t *HealthTool) Run(ctx context.Context, args json.RawMessage) string {
	var a healthArgs
	if len(args) > 0 {
		if errMsg := parseArgs(args, &a); errMsg != "" {
			return errMsg
		}
	}

	depth := a.TreeDepth
	if depth <= 0 {
		depth = 2
	}
	maxEntries := a.TreeMaxEntries
	if maxEntries <= 0 {
		maxEntries = 200
	}

	runtimeCtx := HealthRuntimeContext{}
	if t.ctxFn != nil {
		runtimeCtx = t.ctxFn()
	}

	sessionsRoot := ""
	skillsRoot := ""
	if t.workspace != "" {
		sessionsRoot = filepath.Join(t.workspace, runtimecfg.WorkspaceSessionsDirName)
		skillsRoot = filepath.Join(t.workspace, runtimecfg.WorkspaceSkillsDirName)
	}

	snapshot := healthsnap.Collect(healthsnap.Options{
		Workspace:      t.workspace,
		SessionsRoot:   sessionsRoot,
		SkillsRoot:     skillsRoot,
		ThreadID:       runtimeCtx.ThreadID,
		ThreadType:     runtimeCtx.ThreadType,
		SessionKey:     runtimeCtx.SessionKey,
		SessionFile:    runtimeCtx.SessionFile,
		IncludeTree:    a.IncludeTree,
		TreeDepth:      depth,
		TreeMaxEntries: maxEntries,
	})
	if strings.EqualFold(a.Format, "text") {
		return healthsnap.FormatText(snapshot)
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error: failed to serialize health snapshot: %v", err)
	}
	return string(data)
}
