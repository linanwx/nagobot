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
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
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

Examples:
  nagobot serve              # Start all configured channels
  nagobot serve --telegram   # Start with Telegram bot only
  nagobot serve --discord    # Start with Discord bot only
  nagobot serve --web        # Start Web chat channel only`,
	RunE: runServe,
}

var (
	serveTelegram bool
	serveFeishu   bool
	serveDiscord  bool
	serveWeb      bool
)

func init() {
	serveCmd.Flags().BoolVar(&serveTelegram, "telegram", false, "Enable Telegram bot channel")
	serveCmd.Flags().BoolVar(&serveFeishu, "feishu", false, "Enable Feishu (Lark) bot channel")
	serveCmd.Flags().BoolVar(&serveDiscord, "discord", false, "Enable Discord bot channel")
	serveCmd.Flags().BoolVar(&serveWeb, "web", false, "Enable Web chat channel")
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

	threadMgr, err := buildThreadManager(cfg, true)
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

	// Wire RPC handler so CLI commands can query the running serve process.
	socketCh.SetRPCHandler(func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "sessions.list":
			var p listSessionsOpts
			_ = json.Unmarshal(params, &p)
			if p.Days == 0 {
				p.Days = 2
			}
			// Reload config to respect hot-reload changes.
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
		default:
			return nil, fmt.Errorf("unknown method: %s", method)
		}
	})

	finalServeTelegram, finalServeFeishu, finalServeDiscord, finalServeWeb, err := resolveServeTargets(cmd)
	if err != nil {
		return err
	}

	if finalServeWeb {
		chManager.Register(channel.NewWebChannel(cfg))
	}
	if finalServeTelegram {
		chManager.Register(channel.NewTelegramChannel(cfg))
	}
	if finalServeFeishu {
		chManager.Register(channel.NewFeishuChannel(cfg))
	}
	if finalServeDiscord {
		chManager.Register(channel.NewDiscordChannel(cfg))
	}
	cronCh := channel.NewCronChannel(cfg)
	chManager.Register(cronCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set default agent/sink factories: resolve fallback agent and sink per session key.
	threadMgr.SetDefaultAgentFor(buildDefaultAgentFor(cfg))
	sessionsDir, _ := cfg.SessionsDir()
	threadMgr.SetDefaultSinkFor(buildDefaultSinkFor(chManager, cfg, sessionsDir))

	// Wire system prompt and context budget lookups for the web dashboard.
	if ch, ok := chManager.Get("web"); ok {
		if webCh, ok := ch.(*channel.WebChannel); ok {
			webCh.SetSystemPromptFn(threadMgr.SystemPrompt)
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

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("shutdown signal received")
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

	// Start heartbeat scheduler.
	hbScheduler := newHeartbeatScheduler(threadMgr, func() *config.Config {
		c, _ := config.Load()
		return c
	})
	go hbScheduler.run(ctx)

	// Dispatcher reads from channels and dispatches to threads.
	dispatcher := NewDispatcher(chManager, threadMgr, cfg)

	// Hot-reload: periodically check config for new/removed channel tokens.
	go refreshChannelsLoop(ctx, chManager, dispatcher)

	dispatcher.Run(ctx)

	if err := chManager.StopAll(); err != nil {
		logger.Error("error stopping channels", "err", err)
	}

	logger.Info("nagobot service stopped")
	return nil
}

// buildDefaultAgentFor returns a factory that resolves the default agent name for a given session key.
func buildDefaultAgentFor(cfg *config.Config) func(string) string {
	return func(sessionKey string) string {
		return cfg.SessionAgent(sessionKey)
	}
}

// readDiscordDMReplyTo reads {sessionDir}/channel.json and returns the discord_dm
// reply_to value if present. Returns empty string if not found.
func readDiscordDMReplyTo(sessionsDir, sessionKey string) string {
	parts := strings.Split(sessionKey, ":")
	sessionDir := filepath.Join(append([]string{sessionsDir}, parts...)...)
	data, err := os.ReadFile(filepath.Join(sessionDir, "channel.json"))
	if err != nil {
		return ""
	}
	var routing struct {
		DiscordDM *struct {
			ReplyTo string `json:"reply_to"`
		} `json:"discord_dm"`
	}
	if err := json.Unmarshal(data, &routing); err != nil || routing.DiscordDM == nil {
		return ""
	}
	return routing.DiscordDM.ReplyTo
}

// buildDefaultSinkFor returns a factory that resolves the fallback sink for a given session key.
func buildDefaultSinkFor(chMgr *channel.Manager, cfg *config.Config, sessionsDir string) func(string) thread.Sink {
	return func(sessionKey string) thread.Sink {
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
				if r := readDiscordDMReplyTo(sessionsDir, sessionKey); r != "" {
					replyTo = r
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


func resolveServeTargets(cmd *cobra.Command) (finalServeTelegram, finalServeFeishu, finalServeDiscord, finalServeWeb bool, err error) {
	if cmd == nil {
		return false, false, false, false, fmt.Errorf("serve command is nil")
	}
	flags := cmd.Flags()
	telegramChanged := flags.Changed("telegram")
	feishuChanged := flags.Changed("feishu")
	discordChanged := flags.Changed("discord")
	webChanged := flags.Changed("web")

	// No explicit channel flags -> default to all channels.
	if !telegramChanged && !feishuChanged && !discordChanged && !webChanged {
		return true, true, true, true, nil
	}

	// Any explicit channel flag -> use explicit switches only.
	if telegramChanged {
		finalServeTelegram = serveTelegram
	}
	if feishuChanged {
		finalServeFeishu = serveFeishu
	}
	if discordChanged {
		finalServeDiscord = serveDiscord
	}
	if webChanged {
		finalServeWeb = serveWeb
	}

	if !finalServeTelegram && !finalServeFeishu && !finalServeDiscord && !finalServeWeb {
		return false, false, false, false, fmt.Errorf("no channels enabled; use --telegram, --feishu, --discord, or --web")
	}
	return finalServeTelegram, finalServeFeishu, finalServeDiscord, finalServeWeb, nil
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
