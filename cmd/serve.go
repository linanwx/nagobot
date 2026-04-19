package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start nagobot as a background service",
	Long: `Start nagobot as a long-running daemon that listens on multiple channels.
A unix socket is always started for CLI client connections (nagobot cli).

Supported channels:
  - telegram: Telegram bot
  - feishu: Feishu (Lark) bot
  - discord: Discord bot
  - web: Browser chat UI (http + websocket)
  - wecom: WeCom (WeChat Work) AI Bot

Examples:
  nagobot serve              # Start all configured channels
  nagobot serve --telegram   # Start with Telegram bot only
  nagobot serve --discord    # Start with Discord bot only
  nagobot serve --wecom      # Start with WeCom bot only
  nagobot serve --web        # Start Web chat channel only`,
	RunE: runServe,
}

var (
	serveTelegram bool
	serveFeishu   bool
	serveDiscord  bool
	serveWeb      bool
	serveWeCom    bool
)

func init() {
	serveCmd.Flags().BoolVar(&serveTelegram, "telegram", false, "Enable Telegram bot channel")
	serveCmd.Flags().BoolVar(&serveFeishu, "feishu", false, "Enable Feishu (Lark) bot channel")
	serveCmd.Flags().BoolVar(&serveDiscord, "discord", false, "Enable Discord bot channel")
	serveCmd.Flags().BoolVar(&serveWeb, "web", false, "Enable Web chat channel")
	serveCmd.Flags().BoolVar(&serveWeCom, "wecom", false, "Enable WeCom bot channel")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	installBinary(workspace)

	threadMgr, searchHealthChecker, err := buildThreadManager(cfg, true)
	if err != nil {
		return err
	}
	chManager := channel.NewManager()

	// Socket channel is always started for CLI client connections.
	socketPath, err := config.SocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}
	socketCh := channel.NewSocketChannel(socketPath)
	chManager.Register(socketCh)

	// Heartbeat scheduler (created early so RPC can reference it).
	hbScheduler := newHeartbeatScheduler(threadMgr, func() *config.Config {
		c, _ := config.Load()
		return c
	})

	// shutdownCh allows the RPC "shutdown" method to trigger graceful shutdown.
	shutdownCh := make(chan struct{})

	// Updater for RPC-driven self-update.
	srvUpdater := &updater{}

	// Wire RPC handler so CLI commands can query the running serve process.
	socketCh.SetRPCHandler(func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "update.start":
			var p updateStartParams
			_ = json.Unmarshal(params, &p)
			accepted, reason := srvUpdater.Start(p.Pre)
			return updateStartResponse{Accepted: accepted, Reason: reason, Current: Version}, nil
		case "update.status":
			return srvUpdater.Status(), nil
		case "sessions.list":
			var p listSessionsOpts
			_ = json.Unmarshal(params, &p)
			if p.Days == 0 {
				p.Days = 2
			}
			latestCfg, err := config.Load()
			if err != nil {
				return nil, fmt.Errorf("load config: %w", err)
			}
			output, err := collectSessions(latestCfg, p)
			if err != nil {
				return nil, err
			}
			enrichWithThreads(output, threadMgr.ListThreads())
			return output, nil
		case "heartbeat.status":
			return hbScheduler.Status(), nil
		case "shutdown":
			go func() {
				// Small delay so the RPC response is sent before shutdown.
				time.Sleep(100 * time.Millisecond)
				select {
				case <-shutdownCh:
					// Already closed.
				default:
					close(shutdownCh)
				}
			}()
			return "shutting down", nil
		default:
			return nil, fmt.Errorf("unknown method: %s", method)
		}
	})

	targets, err := resolveServeTargets(cmd)
	if err != nil {
		return err
	}

	if targets.web {
		chManager.Register(channel.NewWebChannel(cfg))
	}
	if targets.telegram {
		chManager.Register(channel.NewTelegramChannel(cfg))
	}
	if targets.feishu {
		chManager.Register(channel.NewFeishuChannel(cfg))
	}
	if targets.discord {
		chManager.Register(channel.NewDiscordChannel(cfg))
	}
	if targets.wecom {
		chManager.Register(channel.NewWeComChannel(cfg))
	}
	cronCh := channel.NewCronChannel(cfg)
	chManager.Register(cronCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set default agent/sink factories: resolve fallback agent and sink per session key.
	threadMgr.SetDefaultAgentFor(buildDefaultAgentFor(threadMgr))
	sessionsDir, _ := cfg.SessionsDir()
	threadMgr.SetDefaultSinkFor(buildDefaultSinkFor(chManager, cfg, sessionsDir,
		func(key string, msg *thread.WakeMessage) { threadMgr.Wake(key, msg) },
		cronCh.FindJob,
	))

	// Wire system prompt and context budget lookups for the web dashboard.
	if ch, ok := chManager.Get("web"); ok {
		if webCh, ok := ch.(*channel.WebChannel); ok {
			webCh.SetSystemPromptFn(threadMgr.SystemPrompt)
			webCh.SetToolDefsFn(threadMgr.ToolDefs)
			webCh.SetContextBudgetFn(threadMgr.ContextBudget)
		}
	}

	// Wire sleep_thread: CronChannel handles DirectWake jobs by waking threads directly.
	cronCh.SetDirectWake(func(sessionKey string, source thread.WakeSource, message string) {
		threadMgr.Wake(sessionKey, &thread.WakeMessage{Source: source, Message: message})
	})

	// Register shared tools.
	threadMgr.RegisterTool(tools.NewWakeThreadTool(threadMgr))
	threadMgr.RegisterTool(tools.NewCheckThreadTool(threadMgr))
	threadMgr.RegisterTool(tools.NewCheckSessionTool(threadMgr))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigChan:
			logger.Info("shutdown signal received")
		case <-shutdownCh:
			logger.Info("shutdown requested via RPC")
		}
		cancel()
	}()

	logger.Info("nagobot is running. Press Ctrl+C to stop.")

	if err := chManager.StartAll(ctx); err != nil {
		return fmt.Errorf("failed to start channels: %w", err)
	}

	// Wire AddJob after StartAll (Scheduler is created in CronChannel.Start).
	threadMgr.SetAddJob(cronCh.AddJob)

	// Start thread manager run loop in background.
	go threadMgr.Run(ctx)

	// Resume interrupted sessions: scan immediately, send wakes after 15s delay
	// to let channels stabilize (so defaultSink can deliver).
	go func() {
		sessionsDir, err := cfg.SessionsDir()
		if err != nil {
			logger.Error("resume: failed to get sessions dir", "err", err)
			return
		}
		candidates := scanInterruptedSessions(sessionsDir)
		if len(candidates) == 0 {
			return
		}
		select {
		case <-time.After(15 * time.Second):
			sendResumeWakes(threadMgr, candidates)
		case <-ctx.Done():
		}
	}()

	// Start heartbeat scheduler (created above near RPC handler).
	go hbScheduler.run(ctx)

	// Set up search health persistence (passive recording, no active probing).
	searchHealthChecker.SetPersistPath(filepath.Join(workspace, "system", "search-health.json"))

	// Start background balance poller.
	balanceCachePath := filepath.Join(workspace, "system", "balance-cache.json")
	metricsDir := filepath.Join(workspace, "metrics")
	balanceCheckers := buildBalanceCheckers(cfg, metricsDir)
	go monitor.RunBalancePoller(ctx, 5*time.Minute, balanceCachePath, balanceCheckers)

	// Dispatcher reads from channels and dispatches to threads.
	dispatcher := NewDispatcher(chManager, threadMgr, cfg)

	// Hot-reload: periodically check config for new/removed channel tokens.
	go refreshChannelsLoop(ctx, chManager, dispatcher)

	dispatcher.Run(ctx)

	threadMgr.Shutdown()

	if err := chManager.StopAll(); err != nil {
		logger.Error("error stopping channels", "err", err)
	}

	logger.Info("nagobot service stopped")
	return nil
}

// buildDefaultAgentFor returns a factory that resolves the default agent name for a given session key.
// Always returns a non-empty name: the persisted agent from meta.json if set, otherwise "soul".
func buildDefaultAgentFor(mgr *thread.Manager) func(string) string {
	return func(sessionKey string) string {
		if name := session.MetaAgent(mgr.SessionDir(sessionKey)); name != "" {
			return name
		}
		return "soul"
	}
}

// readSessionMeta loads meta.json from the session directory.
func readSessionMeta(sessionsDir, sessionKey string) session.Meta {
	return session.ReadMeta(session.SessionDir(sessionsDir, sessionKey))
}

// buildDefaultSinkFor returns a factory that resolves the fallback sink for a given session key.
func buildDefaultSinkFor(chMgr *channel.Manager, cfg *config.Config, sessionsDir string, wakeFn func(string, *thread.WakeMessage), cronJobFn func(string) (cronpkg.Job, bool)) func(string) thread.Sink {
	return func(sessionKey string) thread.Sink {
		// Child threads: route response back to parent thread.
		if idx := strings.Index(sessionKey, ":threads:"); idx >= 0 {
			parentKey := sessionKey[:idx]
			return thread.Sink{
				Label: "your response will be forwarded to parent thread " + parentKey,
				Send: func(ctx context.Context, response string) error {
					if strings.TrimSpace(response) == "" {
						return nil
					}
					wakeMsg := sysmsg.BuildSystemMessage("child_completed", map[string]string{
						"child_session": sessionKey,
					}, strings.TrimSpace(response))
					wakeFn(parentKey, &thread.WakeMessage{
						Source:  thread.WakeChildCompleted,
						Message: wakeMsg,
					})
					return nil
				},
			}
		}

		// Cron jobs: route result to the configured wake_session.
		if strings.HasPrefix(sessionKey, "cron:") {
			jobID := strings.TrimPrefix(sessionKey, "cron:")
			if cronJobFn != nil {
				if job, ok := cronJobFn(jobID); ok && strings.TrimSpace(job.WakeSession) != "" {
					reportTo := strings.TrimSpace(job.WakeSession)
					return thread.Sink{
						Label: "your task will be injected into session " + reportTo + " which will wake, execute, and deliver the result to the user",
						Send: func(ctx context.Context, response string) error {
							if strings.TrimSpace(response) == "" {
								return nil
							}
							wakeMsg := sysmsg.BuildSystemMessage("cron_completed", map[string]string{
								"id": jobID,
							}, strings.TrimSpace(response))
							wakeFn(reportTo, &thread.WakeMessage{
								Source:  thread.WakeCronFinished,
								Message: wakeMsg,
							})
							return nil
						},
					}
				}
			}
			return thread.Sink{Label: "cron silent, result will not be delivered"}
		}

		// telegram:{chatID} or telegram:{userID} → send to that chat.
		if strings.HasPrefix(sessionKey, "telegram:") {
			userID := strings.TrimPrefix(sessionKey, "telegram:")
			if userID != "" {
				return thread.Sink{
					Label:      "your response will be sent to telegram user " + userID,
					Chunkable: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "telegram", response, userID)
					},
				}
			}
		}

		// feishu:{openID} → send to that user via feishu P2P.
		if strings.HasPrefix(sessionKey, "feishu:") {
			openID := strings.TrimPrefix(sessionKey, "feishu:")
			if openID != "" {
				return thread.Sink{
					Label:      "your response will be sent to feishu user " + openID,
					Chunkable: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "feishu", response, "p2p:"+openID)
					},
				}
			}
		}

		// discord:{channelOrUserID} → check channel.json for DM routing, fallback to raw ID.
		if strings.HasPrefix(sessionKey, "discord:") {
			channelID := strings.TrimPrefix(sessionKey, "discord:")
			if channelID != "" {
				replyTo := channelID
				if r := readSessionMeta(sessionsDir, sessionKey); r.DiscordDM != nil && r.DiscordDM.ReplyTo != "" {
					replyTo = r.DiscordDM.ReplyTo
				}
				return thread.Sink{
					Label:      "your response will be sent to discord channel " + channelID,
					Chunkable: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "discord", response, replyTo)
					},
				}
			}
		}

		// wecom:{userID} or wecom:group:{chatID} → send to that user/group.
		if strings.HasPrefix(sessionKey, "wecom:") {
			target := strings.TrimPrefix(sessionKey, "wecom:")
			if target != "" {
				label := "your response will be sent to wecom user " + target
				if strings.HasPrefix(target, "group:") {
					label = "your response will be sent to wecom group " + strings.TrimPrefix(target, "group:")
				}
				// Read persisted req_id once at sink creation (survives restart).
				var persistedReqID string
				if r := readSessionMeta(sessionsDir, sessionKey); r.WeCom != nil {
					persistedReqID = r.WeCom.ReqID
				}
				return thread.Sink{
					Label:     label,
					Chunkable: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendResponse(ctx, "wecom", &channel.Response{
							Text:    response,
							ReplyTo: target,
							Metadata: map[string]string{
								channel.MetaWeComReqID: persistedReqID,
							},
						})
					},
				}
			}
		}

		// "cli" → socket channel.
		if sessionKey == "cli" {
			if _, ok := chMgr.Get("socket"); ok {
				return thread.Sink{
					Label:      "your response will be sent to the CLI client via socket",
					Chunkable: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "socket", response, "")
					},
				}
			}
		}

		return thread.Sink{}
	}
}


type serveTargets struct {
	telegram, feishu, discord, web, wecom bool
}

func resolveServeTargets(cmd *cobra.Command) (serveTargets, error) {
	if cmd == nil {
		return serveTargets{}, fmt.Errorf("serve command is nil")
	}
	flags := cmd.Flags()
	telegramChanged := flags.Changed("telegram")
	feishuChanged := flags.Changed("feishu")
	discordChanged := flags.Changed("discord")
	webChanged := flags.Changed("web")
	wecomChanged := flags.Changed("wecom")

	// No explicit channel flags -> default to all channels.
	if !telegramChanged && !feishuChanged && !discordChanged && !webChanged && !wecomChanged {
		return serveTargets{true, true, true, true, true}, nil
	}

	// Any explicit channel flag -> use explicit switches only.
	var t serveTargets
	if telegramChanged {
		t.telegram = serveTelegram
	}
	if feishuChanged {
		t.feishu = serveFeishu
	}
	if discordChanged {
		t.discord = serveDiscord
	}
	if webChanged {
		t.web = serveWeb
	}
	if wecomChanged {
		t.wecom = serveWeCom
	}

	if !t.telegram && !t.feishu && !t.discord && !t.web && !t.wecom {
		return serveTargets{}, fmt.Errorf("no channels enabled; use --telegram, --feishu, --discord, --web, or --wecom")
	}
	return t, nil
}

// refreshChannelsLoop periodically checks config for new channel tokens and
// dynamically starts/stops channels without restarting the service.
func refreshChannelsLoop(ctx context.Context, chMgr *channel.Manager, dispatcher *Dispatcher) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshChannels(ctx, chMgr, dispatcher)
		}
	}
}

// channelSpec describes a dynamically loadable channel.
type channelSpec struct {
	name      string
	hasToken  func(*config.Config) bool
	newCh     func(*config.Config) channel.Channel
}

var dynamicChannels = []channelSpec{
	{"telegram", func(c *config.Config) bool { return c.GetTelegramToken() != "" }, func(c *config.Config) channel.Channel { return channel.NewTelegramChannel(c) }},
	{"discord", func(c *config.Config) bool { return c.GetDiscordToken() != "" }, func(c *config.Config) channel.Channel { return channel.NewDiscordChannel(c) }},
	{"feishu", func(c *config.Config) bool { return c.GetFeishuAppID() != "" }, func(c *config.Config) channel.Channel { return channel.NewFeishuChannel(c) }},
	{"wecom", func(c *config.Config) bool { return c.GetWeComBotID() != "" }, func(c *config.Config) channel.Channel { return channel.NewWeComChannel(c) }},
}

func refreshChannels(ctx context.Context, chMgr *channel.Manager, dispatcher *Dispatcher) {
	cfg, err := config.Load()
	if err != nil {
		return
	}

	for _, spec := range dynamicChannels {
		registered := chMgr.Has(spec.name)
		configured := spec.hasToken(cfg)

		// Push config updates to running channels.
		if configured && registered {
			if ch, ok := chMgr.Get(spec.name); ok {
				if rc, ok := ch.(channel.Reconfigurable); ok {
					rc.Reconfigure(cfg)
				}
			}
			continue
		}

		if configured && !registered {
			ch := spec.newCh(cfg)
			if ch == nil {
				continue
			}
			if err := ch.Start(ctx); err != nil {
				logger.Warn("hot-reload: failed to start channel", "channel", spec.name, "err", err)
				continue
			}
			chMgr.Register(ch)
			dispatcher.StartDispatching(ch)
			logger.Info("hot-reload: channel started", "channel", spec.name)
		}
	}
}

// installBinary copies the running executable to workspace/bin/nagobot.
// Skips the copy if the destination already has the same file size.
func installBinary(workspace string) {
	exe, err := os.Executable()
	if err != nil {
		logger.Warn("failed to resolve executable path, skipping binary install", "err", err)
		return
	}
	binDir := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		logger.Warn("failed to create bin directory", "err", err)
		return
	}
	dest := filepath.Join(binDir, "nagobot")

	// Skip if same size and not older than the source.
	srcInfo, err := os.Stat(exe)
	if err != nil {
		logger.Warn("failed to stat executable", "err", err)
		return
	}
	if dstInfo, err := os.Stat(dest); err == nil &&
		dstInfo.Size() == srcInfo.Size() &&
		!dstInfo.ModTime().Before(srcInfo.ModTime()) {
		return
	}

	src, err := os.Open(exe)
	if err != nil {
		logger.Warn("failed to open executable for copy", "err", err)
		return
	}
	defer src.Close()

	dst, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		logger.Warn("failed to create bin/nagobot", "err", err)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		logger.Warn("failed to copy executable", "err", err)
		return
	}
	logger.Info("installed binary", "path", dest)
}
