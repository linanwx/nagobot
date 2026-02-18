package channel

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/tgmd"
)

const (
	telegramMessageBufferSize = 100
	TelegramMaxMessageLength  = 4096
)

// TelegramChannel implements the Channel interface for Telegram.
type TelegramChannel struct {
	token      string
	allowedIDs map[int64]bool // Allowed user/chat IDs (nil = allow all)
	messages   chan *Message

	b         *bot.Bot
	cancel    context.CancelFunc
	startDone chan struct{}
}

// NewTelegramChannel creates a new Telegram channel from config.
// Returns nil if no token is configured.
func NewTelegramChannel(cfg *config.Config) Channel {
	token := cfg.GetTelegramToken()
	if token == "" {
		logger.Warn("Telegram token not configured, skipping Telegram channel")
		return nil
	}

	allowedIDs := make(map[int64]bool)
	for _, id := range cfg.GetTelegramAllowedIDs() {
		allowedIDs[id] = true
	}

	return &TelegramChannel{
		token:      token,
		allowedIDs: allowedIDs,
		messages:   make(chan *Message, telegramMessageBufferSize),
	}
}

// Name returns the channel name.
func (t *TelegramChannel) Name() string {
	return "telegram"
}

// Start begins polling for updates.
func (t *TelegramChannel) Start(ctx context.Context) error {
	opts := []bot.Option{
		bot.WithDefaultHandler(t.handleUpdate),
	}

	b, err := bot.New(t.token, opts...)
	if err != nil {
		return fmt.Errorf("telegram bot creation failed: %w", err)
	}
	t.b = b

	me, err := b.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram connection failed: %w", err)
	}
	logger.Info("telegram bot connected", "username", me.Username)

	startCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.startDone = make(chan struct{})

	go func() {
		defer close(t.startDone)
		t.b.Start(startCtx)
	}()

	logger.Info("telegram channel started")
	return nil
}

// Stop gracefully shuts down the channel.
func (t *TelegramChannel) Stop() error {
	if t.cancel != nil {
		t.cancel()
		<-t.startDone
	}
	close(t.messages)
	logger.Info("telegram channel stopped")
	return nil
}

// Send sends a response message.
func (t *TelegramChannel) Send(ctx context.Context, resp *Response) error {
	if t.b == nil {
		return fmt.Errorf("telegram bot not started")
	}

	chatID, err := strconv.ParseInt(resp.ReplyTo, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	chunks := SplitMessage(resp.Text, TelegramMaxMessageLength)

	for _, chunk := range chunks {
		htmlChunk := tgmd.Convert(chunk)
		_, sendErr := t.b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      htmlChunk,
			ParseMode: models.ParseModeHTML,
		})
		if sendErr != nil {
			// Retry without formatting using the original markdown text.
			_, retryErr := t.b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   chunk,
			})
			if retryErr != nil {
				return fmt.Errorf("telegram send error: %w", retryErr)
			}
		}
	}

	return nil
}

// Messages returns the incoming message channel.
func (t *TelegramChannel) Messages() <-chan *Message {
	return t.messages
}
