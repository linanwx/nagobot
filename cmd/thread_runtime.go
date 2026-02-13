package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/thread"
	"github.com/linanwx/nagobot/tools"
)

func buildThreadManager(cfg *config.Config, enableSessions bool) (*thread.Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
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

	skillsDir, err := cfg.SkillsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get skills directory: %w", err)
	}
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions directory: %w", err)
	}

	skillRegistry := skills.NewRegistry()
	if err := skillRegistry.LoadFromDirectory(skillsDir); err != nil {
		logger.Warn("failed to load skills", "dir", skillsDir, "err", err)
	}

	toolRegistry := tools.NewRegistry()
	toolLogsDir := filepath.Join(workspace, "logs", "tool_calls")
	toolRegistry.SetLogsDir(toolLogsDir)
	tools.CleanupLogsDir(toolLogsDir)
	toolRegistry.RegisterDefaultTools(workspace, tools.DefaultToolsConfig{
		ExecTimeout:         cfg.GetExecTimeout(),
		WebSearchMaxResults: cfg.GetWebSearchMaxResults(),
		RestrictToWorkspace: cfg.GetExecRestrictToWorkspace(),
		Skills:              skillRegistry,
	})

	agentRegistry := agent.NewRegistry(workspace)

	var sessions *session.Manager
	if enableSessions {
		sessions, err = session.NewManager(sessionsDir)
		if err != nil {
			logger.Warn("session manager unavailable", "err", err)
		}
	}

	var healthChannels *tools.HealthChannelsInfo
	if ch := cfg.Channels; ch != nil {
		healthChannels = &tools.HealthChannelsInfo{
			AdminUserID: ch.AdminUserID,
			UserAgents:  ch.UserAgents,
		}
		if ch.Telegram != nil {
			healthChannels.Telegram = &tools.HealthTelegramInfo{
				Configured: ch.Telegram.Token != "",
				AllowedIDs: ch.Telegram.AllowedIDs,
			}
		}
		if ch.Web != nil {
			healthChannels.Web = &tools.HealthWebInfo{
				Addr: ch.Web.Addr,
			}
		}
	}

	return thread.NewManager(&thread.ThreadConfig{
		DefaultProvider:     defaultProvider,
		ProviderName:        cfg.Thread.Provider,
		ModelName:           cfg.GetModelName(),
		Tools:               toolRegistry,
		Skills:              skillRegistry,
		Agents:              agentRegistry,
		Workspace:           workspace,
		SkillsDir:           skillsDir,
		SessionsDir:         sessionsDir,
		ContextWindowTokens: cfg.GetContextWindowTokens(),
		ContextWarnRatio:    cfg.GetContextWarnRatio(),
		Sessions:            sessions,
		HealthChannels:      healthChannels,
	}), nil
}
