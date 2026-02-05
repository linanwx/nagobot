package channel

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/linanwx/nagobot/logger"
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
		messages:   make(chan *Message, 100),
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
	u.Timeout = 30
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
	const maxLen = 4096
	messages := splitMessage(resp.Text, maxLen)

	for _, chunk := range messages {
		msg := tgbotapi.NewMessage(chatID, chunk)
		msg.ParseMode = "Markdown"

		if _, err := t.bot.Send(msg); err != nil {
			// Retry without markdown formatting.
			msg.ParseMode = ""
			if _, retryErr := t.bot.Send(msg); retryErr != nil {
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

// processUpdate handles a single update.
func (t *TelegramChannel) processUpdate(update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	msg := update.Message
	chat := msg.Chat
	if chat == nil {
		return
	}

	from := msg.From
	fromID := int64(0)
	username := ""
	firstName := ""
	lastName := ""
	if from != nil {
		fromID = from.ID
		username = from.UserName
		firstName = from.FirstName
		lastName = from.LastName
	}

	if len(t.allowedIDs) > 0 {
		if !t.allowedIDs[chat.ID] && !t.allowedIDs[fromID] {
			logger.Warn("telegram message from unauthorized user",
				"userID", fromID,
				"chatID", chat.ID,
				"username", username,
			)
			return
		}
	}

	// Determine text and media metadata
	text := msg.Text
	metadata := map[string]string{
		"chat_id":    strconv.FormatInt(chat.ID, 10),
		"chat_type":  chat.Type,
		"first_name": firstName,
		"last_name":  lastName,
	}

	switch {
	case len(msg.Photo) > 0:
		// Use the largest photo (last in the slice)
		photo := msg.Photo[len(msg.Photo)-1]
		metadata["media_type"] = "photo"
		metadata["file_id"] = photo.FileID
		if fileURL := t.getFileURL(photo.FileID); fileURL != "" {
			metadata["file_url"] = fileURL
		}
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = "[Photo received]"
		}
	case msg.Document != nil:
		metadata["media_type"] = "document"
		metadata["file_id"] = msg.Document.FileID
		metadata["file_name"] = msg.Document.FileName
		metadata["mime_type"] = msg.Document.MimeType
		if fileURL := t.getFileURL(msg.Document.FileID); fileURL != "" {
			metadata["file_url"] = fileURL
		}
		if text == "" {
			text = msg.Caption
		}
		if text == "" {
			text = fmt.Sprintf("[Document: %s]", msg.Document.FileName)
		}
	case msg.Voice != nil:
		metadata["media_type"] = "voice"
		metadata["file_id"] = msg.Voice.FileID
		metadata["duration"] = strconv.Itoa(msg.Voice.Duration)
		if fileURL := t.getFileURL(msg.Voice.FileID); fileURL != "" {
			metadata["file_url"] = fileURL
		}
		if text == "" {
			text = "[Voice message received]"
		}
	}

	// Skip empty messages (no text and no media)
	if text == "" {
		return
	}

	channelMsg := &Message{
		ID:        strconv.Itoa(msg.MessageID),
		ChannelID: fmt.Sprintf("telegram:%d", chat.ID),
		UserID:    strconv.FormatInt(fromID, 10),
		Username:  username,
		Text:      text,
		Metadata:  metadata,
	}

	if msg.ReplyToMessage != nil {
		channelMsg.ReplyTo = strconv.Itoa(msg.ReplyToMessage.MessageID)
	}

	select {
	case t.messages <- channelMsg:
	default:
		logger.Warn("telegram message buffer full, dropping message")
	}
}

// getFileURL retrieves the download URL for a Telegram file.
func (t *TelegramChannel) getFileURL(fileID string) string {
	if t.bot == nil {
		return ""
	}
	fileCfg := tgbotapi.FileConfig{FileID: fileID}
	file, err := t.bot.GetFile(fileCfg)
	if err != nil {
		logger.Warn("failed to get telegram file URL", "fileID", fileID, "err", err)
		return ""
	}
	return file.Link(t.token)
}

// splitMessage splits a long message into chunks.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to split at newline
		splitAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			splitAt = idx + 1
		}

		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}

	return chunks
}
