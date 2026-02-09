package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
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

	threadMgr, err := buildThreadManager(cfg, true)
	if err != nil {
		return err
	}
	chManager := channel.NewManager()

	finalServeCLI, finalServeTelegram, finalServeWeb, err := resolveServeTargets(cmd)
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
	chManager.Register(channel.NewCronChannel(cfg))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register shared tools.
	threadMgr.RegisterTool(tools.NewWakeThreadTool(threadMgr))
	threadMgr.RegisterTool(tools.NewCheckThreadTool(threadMgr))
	threadMgr.RegisterTool(tools.NewSendMessageTool(chManager, cfg.GetAdminUserID()))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("shutdown signal received")
		cancel()
	}()

	if err := chManager.StartAll(ctx); err != nil {
		return fmt.Errorf("failed to start channels: %w", err)
	}

	// Start thread manager run loop in background.
	go threadMgr.Run(ctx)

	logger.Info("nagobot service started")
	fmt.Println("nagobot is running. Press Ctrl+C to stop.")

	// Dispatcher reads from channels and dispatches to threads. Blocks until ctx done.
	dispatcher := NewDispatcher(chManager, threadMgr, cfg)
	dispatcher.Run(ctx)

	if err := chManager.StopAll(); err != nil {
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

