package cmd

import (
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return fmt.Errorf("telegram connection failed: %w", err)
	}

	chunks := channel.SplitMessage(strings.TrimSpace(sendText), channel.TelegramMaxMessageLength)
	for _, chunk := range chunks {
		htmlChunk := tgmd.Convert(chunk)
		msg := tgbotapi.NewMessage(chatID, htmlChunk)
		msg.ParseMode = tgbotapi.ModeHTML

		if _, err := bot.Send(msg); err != nil {
			// Retry without formatting.
			plainMsg := tgbotapi.NewMessage(chatID, chunk)
			if _, retryErr := bot.Send(plainMsg); retryErr != nil {
				return fmt.Errorf("telegram send error: %w", retryErr)
			}
		}
	}

	fmt.Printf("Message sent to %s\n", to)
	return nil
}
