package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// TelegramChannel implements the Channel interface for Telegram.
type TelegramChannel struct {
	token      string
	apiURL     string
	allowedIDs map[int64]bool // Allowed user/chat IDs (nil = allow all)
	messages   chan *Message
	done       chan struct{}
	wg         sync.WaitGroup
	offset     int64
	client     *http.Client
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
		apiURL:     "https://api.telegram.org/bot" + cfg.Token,
		allowedIDs: allowedIDs,
		messages:   make(chan *Message, 100),
		done:       make(chan struct{}),
		client:     &http.Client{Timeout: 60 * time.Second},
	}
}

// Name returns the channel name.
func (t *TelegramChannel) Name() string {
	return "telegram"
}

// Start begins polling for updates.
func (t *TelegramChannel) Start(ctx context.Context) error {
	// Test connection
	if _, err := t.getMe(); err != nil {
		return fmt.Errorf("telegram connection failed: %w", err)
	}

	logger.Info("telegram channel started")

	t.wg.Add(1)
	go t.pollUpdates(ctx)

	return nil
}

// Stop gracefully shuts down the channel.
func (t *TelegramChannel) Stop() error {
	close(t.done)
	t.wg.Wait()
	close(t.messages)
	logger.Info("telegram channel stopped")
	return nil
}

// Send sends a response message.
func (t *TelegramChannel) Send(ctx context.Context, resp *Response) error {
	chatID, err := strconv.ParseInt(resp.ReplyTo, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	return t.sendMessage(chatID, resp.Text)
}

// Messages returns the incoming message channel.
func (t *TelegramChannel) Messages() <-chan *Message {
	return t.messages
}

// pollUpdates continuously polls for new messages.
func (t *TelegramChannel) pollUpdates(ctx context.Context) {
	defer t.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		default:
			updates, err := t.getUpdates(t.offset, 30)
			if err != nil {
				logger.Error("telegram poll error", "err", err)
				time.Sleep(5 * time.Second)
				continue
			}

			for _, update := range updates {
				t.offset = update.UpdateID + 1
				t.processUpdate(update)
			}
		}
	}
}

// processUpdate handles a single update.
func (t *TelegramChannel) processUpdate(update *telegramUpdate) {
	if update.Message == nil {
		return
	}

	msg := update.Message

	// Check if user is allowed
	if len(t.allowedIDs) > 0 {
		if !t.allowedIDs[msg.Chat.ID] && !t.allowedIDs[msg.From.ID] {
			logger.Warn("telegram message from unauthorized user",
				"userID", msg.From.ID,
				"chatID", msg.Chat.ID,
				"username", msg.From.Username,
			)
			return
		}
	}

	// Convert to our Message type
	channelMsg := &Message{
		ID:        strconv.FormatInt(msg.MessageID, 10),
		ChannelID: fmt.Sprintf("telegram:%d", msg.Chat.ID),
		UserID:    strconv.FormatInt(msg.From.ID, 10),
		Username:  msg.From.Username,
		Text:      msg.Text,
		Metadata: map[string]string{
			"chat_id":    strconv.FormatInt(msg.Chat.ID, 10),
			"chat_type":  msg.Chat.Type,
			"first_name": msg.From.FirstName,
			"last_name":  msg.From.LastName,
		},
	}

	if msg.ReplyToMessage != nil {
		channelMsg.ReplyTo = strconv.FormatInt(msg.ReplyToMessage.MessageID, 10)
	}

	select {
	case t.messages <- channelMsg:
	default:
		logger.Warn("telegram message buffer full, dropping message")
	}
}

// ============================================================================
// Telegram API Methods
// ============================================================================

// getMe tests the bot connection.
func (t *TelegramChannel) getMe() (*telegramUser, error) {
	resp, err := t.client.Get(t.apiURL + "/getMe")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool          `json:"ok"`
		Result *telegramUser `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("telegram API error")
	}

	logger.Info("telegram bot connected", "username", result.Result.Username)
	return result.Result, nil
}

// getUpdates fetches new updates using long polling.
func (t *TelegramChannel) getUpdates(offset int64, timeout int) ([]*telegramUpdate, error) {
	url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=%d", t.apiURL, offset, timeout)

	resp, err := t.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool               `json:"ok"`
		Result []*telegramUpdate  `json:"result"`
		Error  string             `json:"description,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("telegram API error: %s", result.Error)
	}

	return result.Result, nil
}

// sendMessage sends a message to a chat.
func (t *TelegramChannel) sendMessage(chatID int64, text string) error {
	// Split long messages
	const maxLen = 4096
	messages := splitMessage(text, maxLen)

	for _, msg := range messages {
		data := url.Values{
			"chat_id":    {strconv.FormatInt(chatID, 10)},
			"text":       {msg},
			"parse_mode": {"Markdown"},
		}

		resp, err := t.client.PostForm(t.apiURL+"/sendMessage", data)
		if err != nil {
			return err
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			OK    bool   `json:"ok"`
			Error string `json:"description,omitempty"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}

		if !result.OK {
			// Retry without markdown if parsing fails
			if strings.Contains(result.Error, "parse") {
				data.Set("parse_mode", "")
				resp, err = t.client.PostForm(t.apiURL+"/sendMessage", data)
				if err != nil {
					return err
				}
				resp.Body.Close()
			} else {
				return fmt.Errorf("telegram send error: %s", result.Error)
			}
		}
	}

	return nil
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

// ============================================================================
// Telegram Types
// ============================================================================

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type telegramChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
}

type telegramMessage struct {
	MessageID      int64            `json:"message_id"`
	From           *telegramUser    `json:"from"`
	Chat           *telegramChat    `json:"chat"`
	Date           int64            `json:"date"`
	Text           string           `json:"text,omitempty"`
	ReplyToMessage *telegramMessage `json:"reply_to_message,omitempty"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message,omitempty"`
}
