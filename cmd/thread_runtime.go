package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func buildThreadManager(cfg *config.Config, enableSessions bool) (*thread.Manager, *tools.SearchHealthChecker, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get workspace: %w", err)
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
		return nil, nil, fmt.Errorf("failed to create provider factory: %w", err)
	}

	defaultProvider, _ := providerFactory.Create("", "")

	skillsDir, err := cfg.SkillsDir()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get skills directory: %w", err)
	}
	builtinSkillsDir, err := cfg.BuiltinSkillsDir()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get builtin skills directory: %w", err)
	}
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get sessions directory: %w", err)
	}

	skillRegistry := skills.NewRegistry()
	// Load user first, then built-in (built-in overrides stale user copies on name conflict).
	if err := skillRegistry.LoadFromDirectories(skillsDir, builtinSkillsDir); err != nil {
		logger.Warn("failed to load skills", "err", err)
	}

	toolRegistry := tools.NewRegistry()
	toolLogsDir := filepath.Join(workspace, "logs", "tool_calls")
	toolRegistry.SetLogsDir(toolLogsDir)
	tools.CleanupLogsDir(toolLogsDir)
	// Build search providers (all registered; availability checked at call time via Available())
	searchProviders := map[string]tools.SearchProvider{
		"duckduckgo": &tools.DuckDuckGoProvider{},
		"bing":       tools.NewBingProvider(),
		"bing-cn":    tools.NewBingCNProvider(),
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
	zhipuKeyFn := func() string {
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
	}
	zhipuEngines := []struct {
		name   string
		engine string
		tags   []string
	}{
		{"zhipu-cn-std", "search_std", []string{"paid", "¥0.01/query"}},
		{"zhipu-cn-pro", "search_pro", []string{"paid", "¥0.03/query"}},
		{"zhipu-cn-sogou", "search_pro_sogou", []string{"paid", "¥0.05/query"}},
		{"zhipu-cn-quark", "search_pro_quark", []string{"paid", "¥0.05/query"}},
	}
	for _, e := range zhipuEngines {
		searchProviders[e.name] = &tools.ZhipuSearchProvider{
			KeyFn:        zhipuKeyFn,
			ProviderName: e.name,
			Engine:       e.engine,
			ProviderTags: e.tags,
		}
	}

	fetchProviders := map[string]tools.FetchProvider{
		"raw":            &tools.DirectFetchProvider{},
		"go-readability": &tools.ReadabilityFetchProvider{},
		"jina": &tools.JinaFetchProvider{
			KeyFn: func() string {
				c, err := config.Load()
				if err != nil {
					return ""
				}
				return c.GetJinaKey()
			},
		},
		"kimi-cn": &tools.KimiFetchProvider{
			KeyFn: func() string {
				c, err := config.Load()
				if err != nil {
					return ""
				}
				if pc := c.Providers.GetProviderConfig("moonshot-cn"); pc != nil {
					return pc.APIKey
				}
				return ""
			},
			BaseURL:      "https://api.moonshot.cn",
			ProviderTags: []string{"free", "limited-time"},
		},
		"kimi-global": &tools.KimiFetchProvider{
			KeyFn: func() string {
				c, err := config.Load()
				if err != nil {
					return ""
				}
				if pc := c.Providers.GetProviderConfig("moonshot-global"); pc != nil {
					return pc.APIKey
				}
				return ""
			},
			BaseURL:      "https://api.moonshot.ai",
			ProviderTags: []string{"free", "limited-time", "international"},
		},
	}

	metricsDir := filepath.Join(workspace, "metrics")
	metricsStore := monitor.NewStore(metricsDir)
	metricsStore.Rotate()

	var logsDir string
	if cd, err := config.ConfigDir(); err == nil {
		logsDir = filepath.Join(cd, "logs")
	}

	searchHealthChecker := tools.NewSearchHealthChecker(searchProviders)

	fetchHealthChecker := tools.NewFetchHealthChecker(fetchProviders)

	webSearchGuide := ""
	if guideData, err := os.ReadFile(filepath.Join(workspace, "system", "WEB_SEARCH_GUIDE.md")); err == nil {
		webSearchGuide = strings.TrimSpace(string(guideData))
	}
	webFetchGuide := ""
	if guideData, err := os.ReadFile(filepath.Join(workspace, "system", "WEB_FETCH_GUIDE.md")); err == nil {
		webFetchGuide = strings.TrimSpace(string(guideData))
	}

	toolRegistry.RegisterDefaultTools(workspace, tools.DefaultToolsConfig{
		ExecTimeout:         cfg.GetExecTimeout(),
		WebSearchMaxResults: cfg.GetWebSearchMaxResults(),
		WebSearchGuide:      webSearchGuide,
		SearchProviders:     searchProviders,
		SearchHealthChecker: searchHealthChecker,
		FetchProviders:      fetchProviders,
		FetchHealthChecker:  fetchHealthChecker,
		WebFetchGuide:       webFetchGuide,
		RestrictToWorkspace: cfg.GetExecRestrictToWorkspace(),
		Skills:              skillRegistry,
		LogsDir:             logsDir,
	})

	agentRegistry := agent.NewRegistry(workspace)

	var sessions *session.Manager
	if enableSessions {
		sessions, err = session.NewManager(sessionsDir)
		if err != nil {
			logger.Warn("session manager unavailable", "err", err)
		}
		if sessions != nil {
			countsPath := filepath.Join(workspace, "system", "message_counts.json")
			sessions.Counts = session.NewMessageCounts(countsPath)
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
		if ch.Feishu != nil {
			info.Feishu = &tools.HealthFeishuInfo{
				Configured: ch.Feishu.AppID != "",
			}
		}
		if ch.WeCom != nil {
			info.WeCom = &tools.HealthWeComInfo{
				Configured: ch.WeCom.BotID != "",
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
		ContextWindowTokens:  cfg.GetContextWindowTokens(),
		MaxCompletionTokens:  cfg.Thread.MaxTokens,
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
		Sections:            initSectionRegistry(workspace),
	}), searchHealthChecker, nil
}

func initSectionRegistry(workspace string) *agent.SectionRegistry {
	dir := filepath.Join(workspace, "system", "sections")
	reg := agent.NewSectionRegistry(dir)
	reg.Load() // initial load; subsequent reloads happen per-turn via dirSnapshot
	return reg
}
