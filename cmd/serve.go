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

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/logger"
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
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create agent
	ag, err := agent.NewAgent(cfg)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer ag.Close()

	// Create message handler
	handler := func(ctx context.Context, msg *channel.Message) (*channel.Response, error) {
		logger.Info("received message",
			"channel", msg.ChannelID,
			"user", msg.Username,
			"text", truncate(msg.Text, 50),
		)

		// Derive session key from channel and user
		sessionKey := msg.ChannelID
		if msg.UserID != "" {
			sessionKey = msg.ChannelID + ":" + msg.UserID
		}

		// Build user message, injecting media metadata when present
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

		// Attach message origin to context for subagent push delivery.
		// Extract channel name from channelID (e.g., "telegram:123456" -> "telegram")
		originChannel := msg.ChannelID
		if idx := strings.Index(originChannel, ":"); idx > 0 {
			originChannel = originChannel[:idx]
		}
		ctx = channel.WithOrigin(ctx, channel.MessageOrigin{
			Channel:    originChannel,
			ReplyTo:    msg.Metadata["chat_id"],
			SessionKey: sessionKey,
		})

		// Run agent with session history
		response, err := ag.RunInSession(ctx, sessionKey, userMessage)
		if err != nil {
			logger.Error("agent error", "err", err)
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

	// Create router and manager
	router := channel.NewRouter(handler)
	manager := channel.NewManager(router)

	// Register channels
	if serveCLI || (!serveTelegram && !serveAll) {
		manager.Register(channel.NewCLIChannel(channel.CLIConfig{
			Prompt: "nagobot> ",
		}))
		logger.Info("CLI channel enabled")
	}

	if serveTelegram || serveAll {
		token := os.Getenv("TELEGRAM_BOT_TOKEN")
		if token == "" {
			// Try from config
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

	// Wire send_message tool so agent can proactively send to channels
	ag.SetChannelSender(manager)

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("shutdown signal received")
		cancel()
	}()

	// Create and start cron service
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	cronPath := filepath.Join(workspace, "cron.yaml")
	cronSvc, err := cron.NewService(cronPath)
	if err != nil {
		logger.Warn("cron service not started", "err", err)
	}
	if cronSvc != nil {
		cronSvc.Start(func(ctx context.Context, job cron.Job) error {
			logger.Info("cron job triggered", "id", job.ID, "name", job.Name)

			// Delivery mode: send directly to a channel
			if job.Payload.Deliver && job.Payload.Channel != "" {
				if err := manager.SendTo(ctx, job.Payload.Channel, job.Payload.Message, job.Payload.To); err != nil {
					logger.Error("cron delivery failed", "id", job.ID, "channel", job.Payload.Channel, "err", err)
					return err
				}
				return nil
			}

			// Agent mode: process through agent
			_, err := ag.Run(ctx, job.Payload.Message)
			return err
		})
		if err := cronSvc.StartWatching(); err != nil {
			logger.Warn("cron file watcher failed", "err", err)
		}
	}

	// Create and start heartbeat monitor
	heartbeat := cron.NewHeartbeat(cron.HeartbeatConfig{
		Interval: 30 * time.Second,
		OnUnhealthy: func(health *cron.SystemHealth) {
			logger.Error("system unhealthy",
				"status", health.Status,
				"goroutines", health.Goroutines,
				"memoryMB", health.Memory.Alloc/1024/1024,
			)
		},
	})

	// Register health checks
	heartbeat.Register("agent", cron.PingCheck())
	heartbeat.Register("memory", cron.MemoryCheck(500*1024*1024)) // 500MB limit
	heartbeat.Register("goroutines", cron.GoroutineCheck(1000))   // 1000 goroutine limit

	if err := heartbeat.Start(ctx); err != nil {
		return fmt.Errorf("failed to start heartbeat: %w", err)
	}

	// Start heartbeat wakeup: periodically check HEARTBEAT.md for agent tasks.
	// Strictly atomic: rename (claim) first, then read the claimed file.
	// This eliminates the race window between read and rename.
	heartbeatPath := filepath.Join(workspace, "HEARTBEAT.md")
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Step 1: Atomic claim — rename before reading.
				// If the file doesn't exist or rename fails, nothing to do.
				processingPath := heartbeatPath + ".processing"
				if err := os.Rename(heartbeatPath, processingPath); err != nil {
					continue // File doesn't exist or already claimed
				}

				// Step 2: Read the claimed file.
				content, err := os.ReadFile(processingPath)
				if err != nil || len(strings.TrimSpace(string(content))) == 0 {
					_ = os.Remove(processingPath) // Empty or unreadable, discard
					continue
				}
				task := strings.TrimSpace(string(content))

				// Step 3: Execute.
				logger.Info("heartbeat wakeup", "task", truncate(task, 80))
				if _, err := ag.Run(ctx, "Heartbeat task:\n\n"+task); err != nil {
					// Execution failed: preserve as .failed for inspection/retry
					failedPath := heartbeatPath + ".failed"
					_ = os.Rename(processingPath, failedPath)
					logger.Error("heartbeat agent error, task preserved", "err", err, "path", failedPath)
				} else {
					// Success: remove the processing file
					_ = os.Remove(processingPath)
				}
			}
		}
	}()

	// Start all channels
	if err := manager.StartAll(ctx); err != nil {
		return fmt.Errorf("failed to start channels: %w", err)
	}

	logger.Info("nagobot service started")
	fmt.Println("nagobot is running. Press Ctrl+C to stop.")

	// Wait for context cancellation
	<-ctx.Done()

	// Stop all components
	if err := manager.StopAll(); err != nil {
		logger.Error("error stopping channels", "err", err)
	}

	if cronSvc != nil {
		cronSvc.Stop()
	}

	if err := heartbeat.Stop(); err != nil {
		logger.Error("error stopping heartbeat", "err", err)
	}

	logger.Info("nagobot service stopped")
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
