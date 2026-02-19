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
	AgentName   string
}

// HealthContextProvider returns dynamic runtime context.
type HealthContextProvider func() HealthRuntimeContext

// HealthChannelsInfo holds channel config for health output.
type HealthChannelsInfo = healthsnap.ChannelsInfo

// HealthTelegramInfo holds Telegram config for health output.
type HealthTelegramInfo = healthsnap.TelegramInfo

// HealthWebInfo holds Web config for health output.
type HealthWebInfo = healthsnap.WebInfo

// HealthTool reports runtime health info for the current process.
type HealthTool struct {
	Workspace    string
	SessionsRoot string
	SkillsRoot   string
	ProviderName string
	ModelName    string
	Channels      *HealthChannelsInfo
	CtxFn         HealthContextProvider
	ThreadsListFn func() []ThreadInfo
}

// Def returns the tool definition.
func (t *HealthTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "health",
			Description: "Get runtime status of this nagobot process. Returns: LLM provider and model, current time and timezone, Go version/OS/arch, workspace/sessions/skills paths, current thread info (ID, agent name, session key), current session file stats (size, message count), all sessions scan (valid/invalid counts), all active threads, channel config (Telegram allowed IDs, Web addr), cron job list, workspace directory tree, process memory and goroutine count.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"format": map[string]any{
						"type":        "string",
						"description": "Output format: yaml or json. Can be omitted.",
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
		treeDepth      = 1
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
		AgentName:      runtimeCtx.AgentName,
		SessionKey:     runtimeCtx.SessionKey,
		SessionFile:    runtimeCtx.SessionFile,
		Channels:       t.Channels,
		IncludeTree:    true,
		TreeDepth:      treeDepth,
		TreeMaxEntries: treeMaxEntries,
	})

	if t.ThreadsListFn != nil {
		snapshot.AllThreads = t.ThreadsListFn()
	}

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
