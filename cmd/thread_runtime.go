package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/monitor"
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

	cfgFn := func() *config.Config {
		c, err := config.Load()
		if err != nil {
			return cfg // fallback to startup config
		}
		return c
	}
	providerFactory, err := provider.NewFactory(cfgFn)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider factory: %w", err)
	}

	defaultProvider, _ := providerFactory.Create("", "")

	skillsDir, err := cfg.SkillsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get skills directory: %w", err)
	}
	builtinSkillsDir, err := cfg.BuiltinSkillsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get builtin skills directory: %w", err)
	}
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions directory: %w", err)
	}

	skillRegistry := skills.NewRegistry()
	// Load built-in first, then user skills (user overrides built-in on name conflict).
	if err := skillRegistry.LoadFromDirectories(builtinSkillsDir, skillsDir); err != nil {
		logger.Warn("failed to load skills", "err", err)
	}

	toolRegistry := tools.NewRegistry()
	toolLogsDir := filepath.Join(workspace, "logs", "tool_calls")
	toolRegistry.SetLogsDir(toolLogsDir)
	tools.CleanupLogsDir(toolLogsDir)
	// Build search providers (all registered; availability checked at call time via Available())
	searchProviders := map[string]tools.SearchProvider{
		"duckduckgo": &tools.DuckDuckGoProvider{},
		"brave": &tools.BraveSearchProvider{
			KeyFn: func() string {
				c, err := config.Load()
				if err != nil {
					return ""
				}
				return c.GetSearchKey("brave")
			},
		},
		"opensearch": &tools.OpenSearchProvider{
			KeyFn: func() string {
				c, err := config.Load()
				if err != nil {
					return ""
				}
				return c.GetSearchKey("opensearch")
			},
			HostFn: func() string {
				c, err := config.Load()
				if err != nil {
					return ""
				}
				return c.GetSearchKey("opensearch-host")
			},
		},
	}
	searchProviders["zhipu"] = &tools.ZhipuSearchProvider{
		KeyFn: func() string {
			c, err := config.Load()
			if err != nil {
				return ""
			}
			// Prefer dedicated search key; fall back to LLM provider key.
			if k := c.GetSearchKey("zhipu"); k != "" {
				return k
			}
			if pc := c.Providers.GetProviderConfig("zhipu-cn"); pc != nil {
				return pc.APIKey
			}
			return ""
		},
	}

	fetchProviders := map[string]tools.FetchProvider{
		"direct": &tools.DirectFetchProvider{},
		"jina": &tools.JinaFetchProvider{
			KeyFn: func() string {
				c, err := config.Load()
				if err != nil {
					return ""
				}
				return c.GetJinaKey()
			},
		},
	}

	metricsDir := filepath.Join(workspace, "metrics")
	metricsStore := monitor.NewStore(metricsDir)
	metricsStore.Rotate()

	toolRegistry.RegisterDefaultTools(workspace, tools.DefaultToolsConfig{
		ExecTimeout:         cfg.GetExecTimeout(),
		WebSearchMaxResults: cfg.GetWebSearchMaxResults(),
		SearchProviders:     searchProviders,
		FetchProviders:      fetchProviders,
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

	healthChannelsFn := func() *tools.HealthChannelsInfo {
		c := cfgFn()
		ch := c.Channels
		if ch == nil {
			return nil
		}
		info := &tools.HealthChannelsInfo{
			SessionAgents: ch.SessionAgents,
		}
		if ch.Telegram != nil {
			info.Telegram = &tools.HealthTelegramInfo{
				Configured: ch.Telegram.Token != "",
				AllowedIDs: ch.Telegram.AllowedIDs,
			}
		}
		if ch.Discord != nil {
			info.Discord = &tools.HealthDiscordInfo{
				Configured:      ch.Discord.Token != "",
				AllowedGuildIDs: ch.Discord.AllowedGuildIDs,
				AllowedUserIDs:  ch.Discord.AllowedUserIDs,
			}
		}
		if ch.Web != nil {
			info.Web = &tools.HealthWebInfo{
				Addr: ch.Web.Addr,
			}
		}
		return info
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
		BuiltinSkillsDir:    builtinSkillsDir,
		SessionsDir:         sessionsDir,
		ContextWindowTokens: cfg.GetContextWindowTokens(),
		ContextWarnRatio:    cfg.GetContextWarnRatio(),
		Sessions:            sessions,
		HealthChannelsFn:    healthChannelsFn,
		ProviderFactory:     providerFactory,
		Models:              cfg.Thread.Models,
		ModelsFn: func() map[string]*config.ModelConfig {
			c, err := config.Load()
			if err != nil {
				return cfg.Thread.Models
			}
			return c.Thread.Models
		},
		SessionTimezoneFor:  cfg.SessionTimezone,
		MetricsStore:        metricsStore,
	}), nil
}
