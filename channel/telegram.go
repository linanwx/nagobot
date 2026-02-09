package channel

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/linanwx/nagobot/internal/runtimecfg"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/tgmd"
)

// TelegramChannel implements the Channel interface for Telegram.
type TelegramChannel struct {
	token      string
	allowedIDs map[int64]bool // Allowed user/chat IDs (nil = allow all)
	messages   chan *Message
	done       chan struct{}
	wg         sync.WaitGroup

	bot    *tgbotapi.BotAPI
	offset int
}

// TelegramConfig holds Telegram channel configuration.
type TelegramConfig struct {
	Token      string  // Bot token from BotFather
	AllowedIDs []int64 // Allowed user/chat IDs (empty = allow all)
}

// NewTelegramChannel creates a new Telegram channel.
func NewTelegramChannel(cfg TelegramConfig) *TelegramChannel {
	allowedIDs := make(map[int64]bool)
	for _, id := range cfg.AllowedIDs {
		allowedIDs[id] = true
	}

	return &TelegramChannel{
		token:      cfg.Token,
		allowedIDs: allowedIDs,
		messages:   make(chan *Message, runtimecfg.TelegramChannelMessageBufferSize),
		done:       make(chan struct{}),
	}
}

// Name returns the channel name.
func (t *TelegramChannel) Name() string {
	return "telegram"
}

// Start begins polling for updates.
func (t *TelegramChannel) Start(ctx context.Context) error {
	bot, err := tgbotapi.NewBotAPI(t.token)
	if err != nil {
		return fmt.Errorf("telegram connection failed: %w", err)
	}

	me, err := bot.GetMe()
	if err != nil {
		return fmt.Errorf("telegram connection failed: %w", err)
	}

	t.bot = bot
	logger.Info("telegram bot connected", "username", me.UserName)
	logger.Info("telegram channel started")

	u := tgbotapi.NewUpdate(t.offset)
	u.Timeout = runtimecfg.TelegramUpdateTimeoutSeconds
	updates := bot.GetUpdatesChan(u)

	t.wg.Add(1)
	go t.pollUpdates(ctx, updates)

	return nil
}

// Stop gracefully shuts down the channel.
func (t *TelegramChannel) Stop() error {
	close(t.done)
	if t.bot != nil {
		t.bot.StopReceivingUpdates()
	}
	t.wg.Wait()
	close(t.messages)
	logger.Info("telegram channel stopped")
	return nil
}

// Send sends a response message.
func (t *TelegramChannel) Send(ctx context.Context, resp *Response) error {
	if t.bot == nil {
		return fmt.Errorf("telegram bot not started")
	}

	chatID, err := strconv.ParseInt(resp.ReplyTo, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	// Split long messages
	messages := splitMessage(resp.Text, runtimecfg.TelegramMaxMessageLength)

	for _, chunk := range messages {
		htmlChunk := tgmd.Convert(chunk)
		msg := tgbotapi.NewMessage(chatID, htmlChunk)
		msg.ParseMode = tgbotapi.ModeHTML

		if _, err := t.bot.Send(msg); err != nil {
			// Retry without formatting using the original markdown text.
			plainMsg := tgbotapi.NewMessage(chatID, chunk)
			if _, retryErr := t.bot.Send(plainMsg); retryErr != nil {
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

// pollUpdates continuously polls for new messages.
func (t *TelegramChannel) pollUpdates(ctx context.Context, updates tgbotapi.UpdatesChannel) {
	defer t.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.UpdateID >= t.offset {
				t.offset = update.UpdateID + 1
			}
			t.processUpdate(update)
		}
	}
}
