package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	healthsnap "github.com/linanwx/nagobot/internal/health"
	"github.com/linanwx/nagobot/provider"
	"gopkg.in/yaml.v3"
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
	Workspace    string
	SessionsRoot string
	SkillsRoot   string
	ProviderName string
	ModelName    string
	CtxFn        HealthContextProvider
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
						"description": "Output format. Defaults to 'yaml'.",
						"enum":        []string{"yaml", "json"},
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
	if t.CtxFn != nil {
		runtimeCtx = t.CtxFn()
	}

	snapshot := healthsnap.Collect(healthsnap.Options{
		Workspace:      t.Workspace,
		SessionsRoot:   t.SessionsRoot,
		SkillsRoot:     t.SkillsRoot,
		Provider:       t.ProviderName,
		Model:          t.ModelName,
		ThreadID:       runtimeCtx.ThreadID,
		SessionKey:     runtimeCtx.SessionKey,
		SessionFile:    runtimeCtx.SessionFile,
		IncludeTree:    true,
		TreeDepth:      treeDepth,
		TreeMaxEntries: treeMaxEntries,
	})

	if strings.EqualFold(a.Format, "json") {
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error: failed to serialize health snapshot: %v", err)
		}
		return string(data)
	}

	data, err := yaml.Marshal(snapshot)
	if err != nil {
		return fmt.Sprintf("Error: failed to serialize health snapshot: %v", err)
	}
	return string(data)
}
