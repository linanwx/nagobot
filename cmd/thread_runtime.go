package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/internal/runtimecfg"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/thread"
	"github.com/linanwx/nagobot/tools"
)

type threadRuntime struct {
	threadConfig *thread.Config
	toolRegistry *tools.Registry
	soulAgent    *agent.Agent
	workspace    string
}

func buildThreadRuntime(cfg *config.Config, enableSessions bool) (*threadRuntime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	printModelRoutingNotice(cfg)

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}

	providerFactory, err := provider.NewFactory(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider factory: %w", err)
	}

	defaultProvider, err := providerFactory.Create("", "")
	if err != nil {
		return nil, fmt.Errorf("failed to create default provider: %w", err)
	}

	toolRegistry := tools.NewRegistry()
	toolRegistry.RegisterDefaultTools(workspace, tools.DefaultToolsConfig{
		ExecTimeout:         cfg.Tools.Exec.Timeout,
		WebSearchMaxResults: cfg.Tools.Web.Search.MaxResults,
		RestrictToWorkspace: cfg.Tools.Exec.RestrictToWorkspace,
	})

	skillRegistry := skills.NewRegistry()
	skillsDir := filepath.Join(workspace, runtimecfg.WorkspaceSkillsDirName)
	if err := skillRegistry.LoadFromDirectory(skillsDir); err != nil {
		logger.Warn("failed to load skills", "dir", skillsDir, "err", err)
	}
	toolRegistry.Register(tools.NewUseSkillTool(skillRegistry))

	agentRegistry := agent.NewRegistry(workspace)

	var sessions *session.Manager
	if enableSessions {
		sessions, err = session.NewManager(workspace)
		if err != nil {
			logger.Warn("session manager unavailable", "err", err)
		}
	}

	tcfg := &thread.Config{
		DefaultProvider:     defaultProvider,
		ProviderFactory:     providerFactory.Create,
		Tools:               toolRegistry,
		Skills:              skillRegistry,
		Agents:              agentRegistry,
		Workspace:           workspace,
		ContextWindowTokens: cfg.Agents.Defaults.ContextWindowTokens,
		ContextWarnRatio:    cfg.Agents.Defaults.ContextWarnRatio,
		Sessions:            sessions,
	}

	return &threadRuntime{
		threadConfig: tcfg,
		toolRegistry: toolRegistry,
		soulAgent:    agent.NewSoulAgent(workspace),
		workspace:    workspace,
	}, nil
}
