package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start nagobot as a service with channel integrations",
	Long: `Start nagobot as a long-running service that listens on multiple channels.

Supported channels:
  - cli: Interactive command line (default)
  - telegram: Telegram bot (requires TELEGRAM_BOT_TOKEN)
  - feishu: Feishu (Lark) bot (requires FEISHU_APP_ID + FEISHU_APP_SECRET)
  - discord: Discord bot (requires DISCORD_BOT_TOKEN)
  - web: Browser chat UI (http + websocket)

Examples:
  nagobot serve              # Start all configured channels (default)
  nagobot serve --cli        # Start with CLI channel only
  nagobot serve --telegram   # Start with Telegram bot only
  nagobot serve --feishu     # Start with Feishu bot only
  nagobot serve --discord    # Start with Discord bot only
  nagobot serve --web        # Start Web chat channel only`,
	RunE: runServe,
}

var (
	serveTelegram bool
	serveFeishu   bool
	serveDiscord  bool
	serveCLI      bool
	serveWeb      bool
)

func init() {
	serveCmd.Flags().BoolVar(&serveTelegram, "telegram", false, "Enable Telegram bot channel")
	serveCmd.Flags().BoolVar(&serveFeishu, "feishu", false, "Enable Feishu (Lark) bot channel")
	serveCmd.Flags().BoolVar(&serveDiscord, "discord", false, "Enable Discord bot channel")
	serveCmd.Flags().BoolVar(&serveWeb, "web", false, "Enable Web chat channel")

	serveCmd.Flags().BoolVar(&serveCLI, "cli", true, "Enable CLI channel (default: true)")
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

	finalServeCLI, finalServeTelegram, finalServeFeishu, finalServeDiscord, finalServeWeb, err := resolveServeTargets(cmd)
	if err != nil {
		return err
	}

	if finalServeWeb {
		chManager.Register(channel.NewWebChannel(cfg))
	}
	if finalServeCLI {
		chManager.Register(channel.NewCLIChannel())
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
	chManager.Register(channel.NewCronChannel(cfg))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set default agent/sink factories: resolve fallback agent and sink per session key.
	threadMgr.SetDefaultAgentFor(buildDefaultAgentFor(cfg))
	threadMgr.SetDefaultSinkFor(buildDefaultSinkFor(chManager, cfg))

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

	// Start thread manager run loop in background.
	go threadMgr.Run(ctx)

	// Dispatcher reads from channels and dispatches to threads. Blocks until ctx done.
	dispatcher := NewDispatcher(chManager, threadMgr, cfg)
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
		if cfg.Channels == nil || len(cfg.Channels.SessionAgents) == 0 {
			return ""
		}
		return cfg.Channels.SessionAgents[sessionKey]
	}
}

// buildDefaultSinkFor returns a factory that resolves the fallback sink for a given session key.
func buildDefaultSinkFor(chMgr *channel.Manager, cfg *config.Config) func(string) thread.Sink {
	return func(sessionKey string) thread.Sink {
		// telegram:{chatID}[:{userID}[:threads:...]] → send to that chat.
		if strings.HasPrefix(sessionKey, "telegram:") {
			userID := extractFirstSegment(strings.TrimPrefix(sessionKey, "telegram:"))
			if userID != "" {
				return thread.Sink{
					Label:      "your response will be sent to telegram user " + userID,
					Idempotent: true,
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
		// Design: all feishu sessions (including group messages) are per-user.
		// The per-wake sink from buildSink uses the original chat_id for reply routing,
		// while this fallback sink (for cron/spawn) always replies via P2P.
		if strings.HasPrefix(sessionKey, "feishu:") {
			openID := extractFirstSegment(strings.TrimPrefix(sessionKey, "feishu:"))
			if openID != "" {
				return thread.Sink{
					Label:      "your response will be sent to feishu user " + openID,
					Idempotent: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "feishu", response, "p2p:"+openID)
					},
				}
			}
		}

		// discord:{channelID} → send to that channel.
		if strings.HasPrefix(sessionKey, "discord:") {
			channelID := strings.TrimPrefix(sessionKey, "discord:")
			if channelID != "" {
				return thread.Sink{
					Label:      "your response will be sent to discord channel " + channelID,
					Idempotent: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "discord", response, channelID)
					},
				}
			}
		}

		// "cli" → cli only.
		if sessionKey == "cli" {
			if _, ok := chMgr.Get("cli"); ok {
				return thread.Sink{
					Label:      "your response will be printed to cli",
					Idempotent: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "cli", response, "")
					},
				}
			}
		}

		return thread.Sink{}
	}
}

// extractFirstSegment returns the part before the first ':' in s, or all of s if no ':' exists.
func extractFirstSegment(s string) string {
	if idx := strings.Index(s, ":"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func resolveServeTargets(cmd *cobra.Command) (finalServeCLI, finalServeTelegram, finalServeFeishu, finalServeDiscord, finalServeWeb bool, err error) {
	if cmd == nil {
		return false, false, false, false, false, fmt.Errorf("serve command is nil")
	}
	flags := cmd.Flags()
	cliChanged := flags.Changed("cli")
	telegramChanged := flags.Changed("telegram")
	feishuChanged := flags.Changed("feishu")
	discordChanged := flags.Changed("discord")
	webChanged := flags.Changed("web")

	// No explicit channel flags -> default to all channels.
	if !cliChanged && !telegramChanged && !feishuChanged && !discordChanged && !webChanged {
		return true, true, true, true, true, nil
	}

	// Any explicit channel flag -> use explicit switches only.
	if cliChanged {
		finalServeCLI = serveCLI
	}
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

	if !finalServeCLI && !finalServeTelegram && !finalServeFeishu && !finalServeDiscord && !finalServeWeb {
		return false, false, false, false, false, fmt.Errorf("no channels enabled; use --cli, --telegram, --feishu, --discord, or --web")
	}
	return finalServeCLI, finalServeTelegram, finalServeFeishu, finalServeDiscord, finalServeWeb, nil
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

	// Skip if same size.
	srcInfo, err := os.Stat(exe)
	if err != nil {
		logger.Warn("failed to stat executable", "err", err)
		return
	}
	if dstInfo, err := os.Stat(dest); err == nil && dstInfo.Size() == srcInfo.Size() {
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
