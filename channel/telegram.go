package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	mediaDir   string // Local directory for downloaded media files

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

	var mediaDir string
	if ws, err := cfg.WorkspacePath(); err == nil {
		mediaDir = filepath.Join(ws, "media")
		if err := os.MkdirAll(mediaDir, 0755); err != nil {
			logger.Warn("failed to create media directory", "dir", mediaDir, "err", err)
			mediaDir = ""
		}
	}

	return &TelegramChannel{
		token:      token,
		allowedIDs: allowedIDs,
		messages:   make(chan *Message, telegramMessageBufferSize),
		mediaDir:   mediaDir,
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
		bot.WithErrorsHandler(func(err error) {
			logger.Error("telegram bot error", "error", err)
		}),
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

// downloadToMedia downloads a URL to the media directory, returning the local path.
// Returns empty string on error (caller should fall back to URL).
func (t *TelegramChannel) downloadToMedia(url string) string {
	if t.mediaDir == "" || url == "" {
		return ""
	}

	resp, err := http.Get(url)
	if err != nil {
		logger.Warn("failed to download media", "url", url, "err", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("media download returned non-200", "url", url, "status", resp.StatusCode)
		return ""
	}

	// Detect extension: try URL path first, then Content-Type, then fallback.
	ext := filepath.Ext(url)
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		// URL already has a recognized extension.
	default:
		ct := resp.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, "image/jpeg"):
			ext = ".jpg"
		case strings.HasPrefix(ct, "image/png"):
			ext = ".png"
		case strings.HasPrefix(ct, "image/gif"):
			ext = ".gif"
		case strings.HasPrefix(ct, "image/webp"):
			ext = ".webp"
		default:
			ext = ".dat"
		}
	}

	buf := make([]byte, 4)
	rand.Read(buf)
	fileName := fmt.Sprintf("img-%s-%s%s", time.Now().Format("20060102-150405"), hex.EncodeToString(buf), ext)
	filePath := filepath.Join(t.mediaDir, fileName)

	f, err := os.Create(filePath)
	if err != nil {
		logger.Warn("failed to create media file", "path", filePath, "err", err)
		return ""
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		logger.Warn("failed to write media file", "path", filePath, "err", err)
		os.Remove(filePath)
		return ""
	}

	return filePath
}
