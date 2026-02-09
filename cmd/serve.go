package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/internal/runtimecfg"
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
  - web: Browser chat UI (http + websocket)

Examples:
  nagobot serve              # Start with CLI channel
  nagobot serve --telegram   # Start with Telegram bot
  nagobot serve --web        # Start Web chat channel
  nagobot serve --all        # Start all configured channels`,
	RunE: runServe,
}

var (
	serveTelegram bool
	serveAll      bool
	serveCLI      bool
	serveWeb      bool
)

func init() {
	serveCmd.Flags().BoolVar(&serveTelegram, "telegram", false, "Enable Telegram bot channel")
	serveCmd.Flags().BoolVar(&serveWeb, "web", false, "Enable Web chat channel")
	serveCmd.Flags().BoolVar(&serveAll, "all", false, "Enable all configured channels")
	serveCmd.Flags().BoolVar(&serveCLI, "cli", true, "Enable CLI channel (default: true)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	tcfg, err := buildThreadConfig(cfg, true)
	if err != nil {
		return err
	}
	threadMgr := thread.NewManager(tcfg)
	manager := channel.NewManager()

	finalServeCLI, finalServeTelegram, finalServeWeb, err := resolveServeTargets(cmd)
	if err != nil {
		return err
	}

	if finalServeWeb {
		addr := runtimecfg.WebChannelDefaultAddr
		if cfg.Channels != nil && cfg.Channels.Web != nil {
			if configuredAddr := strings.TrimSpace(cfg.Channels.Web.Addr); configuredAddr != "" {
				addr = configuredAddr
			}
		}
		manager.Register(channel.NewWebChannel(channel.WebConfig{
			Addr:      addr,
			Workspace: tcfg.Workspace,
		}))
		logger.Info("Web channel enabled", "addr", addr)
	}

	if finalServeCLI {
		manager.Register(channel.NewCLIChannel(channel.CLIConfig{Prompt: "nagobot> "}))
		logger.Info("CLI channel enabled")
	}

	if finalServeTelegram {
		token := os.Getenv("TELEGRAM_BOT_TOKEN")
		if token == "" {
			if cfg.Channels != nil && cfg.Channels.Telegram != nil {
				token = cfg.Channels.Telegram.Token
			}
		}

		if token == "" {
			logger.Warn("Telegram token not configured, skipping Telegram channel")
		} else {
			var allowedIDs []int64
			if cfg.Channels != nil && cfg.Channels.Telegram != nil {
				allowedIDs = cfg.Channels.Telegram.AllowedIDs
			}

			manager.Register(channel.NewTelegramChannel(channel.TelegramConfig{
				Token:      token,
				AllowedIDs: allowedIDs,
			}))
			logger.Info("Telegram channel enabled")
		}
	}

	// Register cron channel.
	cronStorePath := filepath.Join(tcfg.Workspace, "cron.yaml")
	manager.Register(channel.NewCronChannel(cronStorePath))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register shared tools.
	tcfg.Tools.Register(tools.NewWakeThreadTool(threadMgr))
	adminUserID := ""
	if cfg.Channels != nil {
		adminUserID = cfg.Channels.AdminUserID
	}
	tcfg.Tools.Register(tools.NewSendMessageTool(manager, adminUserID))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("shutdown signal received")
		cancel()
	}()

	if err := manager.StartAll(ctx); err != nil {
		return fmt.Errorf("failed to start channels: %w", err)
	}

	// Start thread manager run loop in background.
	go threadMgr.Run(ctx)

	logger.Info("nagobot service started")
	fmt.Println("nagobot is running. Press Ctrl+C to stop.")

	// Dispatcher reads from channels and dispatches to threads. Blocks until ctx done.
	dispatcher := NewDispatcher(manager, threadMgr, cfg)
	dispatcher.Run(ctx)

	if err := manager.StopAll(); err != nil {
		logger.Error("error stopping channels", "err", err)
	}

	logger.Info("nagobot service stopped")
	return nil
}

func resolveServeTargets(cmd *cobra.Command) (finalServeCLI, finalServeTelegram, finalServeWeb bool, err error) {
	if cmd == nil {
		return false, false, false, fmt.Errorf("serve command is nil")
	}
	if serveAll {
		return true, true, true, nil
	}

	flags := cmd.Flags()
	cliChanged := flags.Changed("cli")
	telegramChanged := flags.Changed("telegram")
	webChanged := flags.Changed("web")

	// No explicit channel flags -> default to CLI only.
	if !cliChanged && !telegramChanged && !webChanged {
		return true, false, false, nil
	}

	// Any explicit channel flag -> use explicit switches only.
	if cliChanged {
		finalServeCLI = serveCLI
	}
	if telegramChanged {
		finalServeTelegram = serveTelegram
	}
	if webChanged {
		finalServeWeb = serveWeb
	}

	if !finalServeCLI && !finalServeTelegram && !finalServeWeb {
		return false, false, false, fmt.Errorf("no channels enabled; use --cli, --telegram, --web, or --all")
	}
	return finalServeCLI, finalServeTelegram, finalServeWeb, nil
}

