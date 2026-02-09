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
			Description: "Get runtime health information for this nagobot process, including paths, thread/session diagnostics, and workspace tree snapshot.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"format": map[string]any{
						"type":        "string",
						"description": "Optional output format: 'json' or 'text'. Defaults to 'json'.",
						"enum":        []string{"json", "text"},
					},
				},
			},
		},
	}
}

type healthArgs struct {
	Format string `json:"format,omitempty"`
}

// Run executes the tool.
func (t *HealthTool) Run(ctx context.Context, args json.RawMessage) string {
	var a healthArgs
	if len(args) > 0 {
		if errMsg := parseArgs(args, &a); errMsg != "" {
			return errMsg
		}
	}

	const (
		treeDepth      = 3
		treeMaxEntries = 200
	)

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
		SessionKey:     runtimeCtx.SessionKey,
		SessionFile:    runtimeCtx.SessionFile,
		IncludeTree:    true,
		TreeDepth:      treeDepth,
		TreeMaxEntries: treeMaxEntries,
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
