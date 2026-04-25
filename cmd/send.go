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
	"github.com/linanwx/nagobot/tools"
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
	sendCmd.Flags().StringVar(&sendTo, "to", "", "Telegram chat/user ID (required)")
	sendCmd.Flags().StringVar(&sendText, "text", "", "Message text (required)")
	_ = sendCmd.MarkFlagRequired("to")
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
		return fmt.Errorf("--to is required.\nFix: nagobot send --to <chat_id> --text \"message\"")
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
	var lastMsgID int
	for _, chunk := range chunks {
		htmlChunk := tgmd.Convert(chunk)
		resp, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      htmlChunk,
			ParseMode: models.ParseModeHTML,
		})
		if sendErr != nil {
			// Retry without formatting.
			resp, retryErr := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   chunk,
			})
			if retryErr != nil {
				return fmt.Errorf("telegram send error: %w", retryErr)
			}
			if resp != nil {
				lastMsgID = resp.ID
			}
		} else if resp != nil {
			lastMsgID = resp.ID
		}
	}

	pairs := [][2]string{
		{"command", "send"}, {"status", "ok"},
		{"recipient", to}, {"chunks", fmt.Sprintf("%d", len(chunks))},
	}
	if lastMsgID > 0 {
		pairs = append(pairs, [2]string{"message_id", fmt.Sprintf("%d", lastMsgID)})
	}
	fmt.Print(tools.CmdOutput(pairs, ""))
	return nil
}
