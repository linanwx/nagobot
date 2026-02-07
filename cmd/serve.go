package cmd

import (
	"context"
	"fmt"
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

Examples:
  nagobot serve              # Start with CLI channel
  nagobot serve --telegram   # Start with Telegram bot
  nagobot serve --all        # Start all configured channels`,
	RunE: runServe,
}

var (
	serveTelegram bool
	serveAll      bool
	serveCLI      bool
)

func init() {
	serveCmd.Flags().BoolVar(&serveTelegram, "telegram", false, "Enable Telegram bot channel")
	serveCmd.Flags().BoolVar(&serveAll, "all", false, "Enable all configured channels")
	serveCmd.Flags().BoolVar(&serveCLI, "cli", true, "Enable CLI channel (default: true)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	rt, err := buildThreadRuntime(cfg, true)
	if err != nil {
		return err
	}
	threadMgr := thread.NewManager(rt.threadConfig)
	var manager *channel.Manager

	handler := func(ctx context.Context, msg *channel.Message) (*channel.Response, error) {
		logger.Debug("received message",
			"channel", msg.ChannelID,
			"user", msg.Username,
			"text", truncate(msg.Text, 50),
		)

		sessionKey := buildSessionKey(msg, cfg)

		userMessage := msg.Text
		if mediaType := msg.Metadata["media_type"]; mediaType != "" {
			var mediaParts []string
			mediaParts = append(mediaParts, fmt.Sprintf("media_type: %s", mediaType))
			if fn := msg.Metadata["file_name"]; fn != "" {
				mediaParts = append(mediaParts, fmt.Sprintf("file_name: %s", fn))
			}
			if mime := msg.Metadata["mime_type"]; mime != "" {
				mediaParts = append(mediaParts, fmt.Sprintf("mime_type: %s", mime))
			}
			if url := msg.Metadata["file_url"]; url != "" {
				mediaParts = append(mediaParts, fmt.Sprintf("file_url: %s", url))
			}
			if dur := msg.Metadata["duration"]; dur != "" {
				mediaParts = append(mediaParts, fmt.Sprintf("duration: %ss", dur))
			}
			userMessage = fmt.Sprintf("[Media: %s]\n%s\n\n%s", mediaType, strings.Join(mediaParts, "\n"), msg.Text)
		}

		t := threadMgr.GetOrCreateChannel(sessionKey, rt.soulAgent, buildThreadSink(manager, msg))
		response, err := t.Run(thread.WithoutSink(ctx), userMessage)
		if err != nil {
			logger.Error("thread error", "err", err)
			return &channel.Response{
				Text:    fmt.Sprintf("Error: %v", err),
				ReplyTo: msg.Metadata["chat_id"],
			}, nil
		}

		return &channel.Response{
			Text:    response,
			ReplyTo: msg.Metadata["chat_id"],
		}, nil
	}

	manager = channel.NewManager(handler)

	if serveCLI || (!serveTelegram && !serveAll) {
		manager.Register(channel.NewCLIChannel(channel.CLIConfig{Prompt: "nagobot> "}))
		logger.Info("CLI channel enabled")
	}

	if serveTelegram || serveAll {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// wake_thread and send_message are shared, and each thread clones this registry at runtime.
	rt.toolRegistry.Register(tools.NewWakeThreadTool(threadMgr))
	rt.toolRegistry.Register(tools.NewSendMessageTool(manager))

	cronStorePath := filepath.Join(rt.workspace, "cron.json")
	scheduler := cronpkg.NewScheduler(cronStorePath, func(job *cronpkg.Job) (string, error) {
		ag := rt.soulAgent
		if job != nil && rt.threadConfig != nil && rt.threadConfig.Agents != nil {
			if agentName := strings.TrimSpace(job.Agent); agentName != "" {
				if named := rt.threadConfig.Agents.Get(agentName); named != nil {
					ag = named
				}
			}
		}
		task := ""
		if job != nil {
			task = strings.TrimSpace(job.Task)
		}
		ag = thread.WrapAgentTaskPlaceholder(ag, task)
		t := thread.NewChannel(rt.threadConfig, ag, buildCronSessionKey(job), nil)
		return t.Run(ctx, task)
	})
	if err := scheduler.Load(); err != nil {
		return fmt.Errorf("failed to load cron jobs: %w", err)
	}
	scheduler.Start()
	defer scheduler.Stop()
	rt.toolRegistry.Register(tools.NewManageCronTool(scheduler))

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

	logger.Info("nagobot service started")
	fmt.Println("nagobot is running. Press Ctrl+C to stop.")

	<-ctx.Done()

	if err := manager.StopAll(); err != nil {
		logger.Error("error stopping channels", "err", err)
	}

	logger.Info("nagobot service stopped")
	return nil
}

func buildSessionKey(msg *channel.Message, cfg *config.Config) string {
	if msg == nil {
		return "main"
	}

	if msg.ChannelID == "cli:local" {
		return "main"
	}

	if strings.HasPrefix(msg.ChannelID, "telegram:") {
		userID := strings.TrimSpace(msg.UserID)
		adminID := ""
		if cfg != nil && cfg.Channels != nil {
			adminID = strings.TrimSpace(cfg.Channels.AdminUserID)
		}
		if userID != "" && adminID != "" && userID == adminID {
			return "main"
		}
		if userID != "" {
			return "telegram:" + userID
		}
		return msg.ChannelID
	}

	sessionKey := msg.ChannelID
	if msg.UserID != "" {
		sessionKey = msg.ChannelID + ":" + msg.UserID
	}
	return sessionKey
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func buildThreadSink(manager *channel.Manager, msg *channel.Message) thread.Sink {
	if manager == nil || msg == nil {
		return nil
	}

	channelName := channelNameFromID(msg.ChannelID)
	if channelName == "" {
		return nil
	}

	replyTo := strings.TrimSpace(msg.Metadata["chat_id"])
	if replyTo == "" {
		replyTo = strings.TrimSpace(msg.ReplyTo)
	}

	return func(ctx context.Context, response string) error {
		if strings.TrimSpace(response) == "" {
			return nil
		}
		return manager.SendTo(ctx, channelName, response, replyTo)
	}
}

func channelNameFromID(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ""
	}
	if idx := strings.Index(channelID, ":"); idx > 0 {
		return channelID[:idx]
	}
	return channelID
}

func buildCronSessionKey(job *cronpkg.Job) string {
	jobID := "job"
	if job != nil && strings.TrimSpace(job.ID) != "" {
		jobID = strings.TrimSpace(job.ID)
	}

	now := time.Now().UTC()
	suffix := thread.RandomHex(4)
	if suffix == "" {
		suffix = fmt.Sprintf("%d", now.UnixNano())
	}

	return fmt.Sprintf(
		"cron:%s:threads:%s:%s-%s",
		jobID,
		now.Format("2006-01-02"),
		now.Format("20060102T150405Z"),
		suffix,
	)
}
