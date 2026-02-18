package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/tgmd"
	"github.com/spf13/cobra"
)

var sendCmd = &cobra.Command{
	Use:     "send",
	Short:   "Send a message to Telegram",
	GroupID: "internal",
	RunE:    runSend,
}

var (
	sendTo   string
	sendText string
)

func init() {
	sendCmd.Flags().StringVar(&sendTo, "to", "", "Telegram chat/user ID (defaults to admin user ID)")
	sendCmd.Flags().StringVar(&sendText, "text", "", "Message text (required)")
	_ = sendCmd.MarkFlagRequired("text")
	rootCmd.AddCommand(sendCmd)
}

func runSend(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	token := cfg.GetTelegramToken()
	if token == "" {
		return fmt.Errorf("telegram bot token not configured")
	}

	to := strings.TrimSpace(sendTo)
	if to == "" {
		to = strings.TrimSpace(cfg.GetAdminUserID())
	}
	if to == "" {
		return fmt.Errorf("--to is required (no admin user ID configured as fallback)")
	}

	chatID, err := strconv.ParseInt(to, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID %q: %w", to, err)
	}

	b, err := bot.New(token, bot.WithSkipGetMe())
	if err != nil {
		return fmt.Errorf("telegram bot creation failed: %w", err)
	}

	ctx := context.Background()
	chunks := channel.SplitMessage(strings.TrimSpace(sendText), channel.TelegramMaxMessageLength)
	for _, chunk := range chunks {
		htmlChunk := tgmd.Convert(chunk)
		_, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      htmlChunk,
			ParseMode: models.ParseModeHTML,
		})
		if sendErr != nil {
			// Retry without formatting.
			_, retryErr := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   chunk,
			})
			if retryErr != nil {
				return fmt.Errorf("telegram send error: %w", retryErr)
			}
		}
	}

	fmt.Printf("Message sent to %s\n", to)
	return nil
}
