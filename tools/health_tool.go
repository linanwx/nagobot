package tools

import (
	"context"
	"encoding/json"
	"fmt"
	healthsnap "github.com/linanwx/nagobot/internal/health"
	"github.com/linanwx/nagobot/provider"
	"gopkg.in/yaml.v3"
)

// HealthRuntimeContext is thread/session metadata injected at runtime.
type HealthRuntimeContext struct {
	ThreadID     string
	SessionKey   string
	SessionFile  string
	AgentName    string
	ProviderName string
	ModelName    string
}

// HealthContextProvider returns dynamic runtime context.
type HealthContextProvider func() HealthRuntimeContext

// HealthChannelsInfo holds channel config for health output.
type HealthChannelsInfo = healthsnap.ChannelsInfo

// HealthTelegramInfo holds Telegram config for health output.
type HealthTelegramInfo = healthsnap.TelegramInfo

// HealthDiscordInfo holds Discord config for health output.
type HealthDiscordInfo = healthsnap.DiscordInfo

// HealthFeishuInfo holds Feishu config for health output.
type HealthFeishuInfo = healthsnap.FeishuInfo

// HealthWeComInfo holds WeCom config for health output.
type HealthWeComInfo = healthsnap.WeComInfo

// HealthWebInfo holds Web config for health output.
type HealthWebInfo = healthsnap.WebInfo

// HealthTool reports runtime health info for the current process.
type HealthTool struct {
	Workspace     string
	SessionsRoot  string
	SkillsRoot    string
	LogsDir       string // Log files directory (e.g. ~/.nagobot/logs)
	ProviderName  string // Fallback; overridden by CtxFn if set.
	ModelName     string // Fallback; overridden by CtxFn if set.
	ChannelsFn    func() *HealthChannelsInfo
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
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *HealthTool) channels() *HealthChannelsInfo {
	if t.ChannelsFn != nil {
		return t.ChannelsFn()
	}
	return nil
}

// Run executes the tool.
func (t *HealthTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "health", healthToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *HealthTool) run(ctx context.Context, _ json.RawMessage) string {
	const (
		treeDepth      = 1
		treeMaxEntries = 200
	)

	runtimeCtx := HealthRuntimeContext{}
	if t.CtxFn != nil {
		runtimeCtx = t.CtxFn()
	}

	providerName, modelName := t.ProviderName, t.ModelName
	if runtimeCtx.ProviderName != "" {
		providerName = runtimeCtx.ProviderName
	}
	if runtimeCtx.ModelName != "" {
		modelName = runtimeCtx.ModelName
	}

	snapshot := healthsnap.Collect(ctx, healthsnap.Options{
		Workspace:      t.Workspace,
		SessionsRoot:   t.SessionsRoot,
		SkillsRoot:     t.SkillsRoot,
		Provider:       providerName,
		Model:          modelName,
		ThreadID:       runtimeCtx.ThreadID,
		AgentName:      runtimeCtx.AgentName,
		SessionKey:     runtimeCtx.SessionKey,
		SessionFile:    runtimeCtx.SessionFile,
		Channels:       t.channels(),
		LogsDir:        t.LogsDir,
		IncludeTree:    true,
		TreeDepth:      treeDepth,
		TreeMaxEntries: treeMaxEntries,
	})

	if t.ThreadsListFn != nil {
		snapshot.AllThreads = t.ThreadsListFn()
	}

	data, err := yaml.Marshal(snapshot)
	if err != nil {
		return fmt.Sprintf("Error: failed to serialize health snapshot: %v", err)
	}
	return string(data)
}
