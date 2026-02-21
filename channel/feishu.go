package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-lark/lark"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const (
	feishuMessageBufferSize = 100
	feishuMaxMessageLength  = 4000
	feishuMaxBodySize       = 1 << 20 // 1MB
	feishuDedupTTL          = 5 * time.Minute
)

// FeishuChannel implements the Channel interface for Feishu (Lark).
type FeishuChannel struct {
	appID, appSecret  string
	verificationToken string
	encryptKey        string
	webhookAddr       string
	allowedOpenIDs    map[string]bool // nil or empty = allow all
	bot               *lark.Bot
	server            *http.Server
	messages          chan *Message
	done              chan struct{}
	wg                sync.WaitGroup
	encryptedKey      []byte // precomputed from encryptKey

	// Event dedup: Feishu retries delivery, so we track seen event IDs.
	seenMu   sync.Mutex
	seen     map[string]time.Time
	stopOnce sync.Once
}

// NewFeishuChannel creates a new Feishu channel from config.
// Returns nil if AppID or AppSecret is not configured.
func NewFeishuChannel(cfg *config.Config) Channel {
	appID := cfg.GetFeishuAppID()
	appSecret := cfg.GetFeishuAppSecret()
	if appID == "" || appSecret == "" {
		logger.Warn("Feishu appId/appSecret not configured, skipping Feishu channel")
		return nil
	}

	allowedOpenIDs := make(map[string]bool)
	for _, id := range cfg.GetFeishuAllowedOpenIDs() {
		allowedOpenIDs[id] = true
	}

	ch := &FeishuChannel{
		appID:             appID,
		appSecret:         appSecret,
		verificationToken: cfg.GetFeishuVerificationToken(),
		encryptKey:        cfg.GetFeishuEncryptKey(),
		webhookAddr:       cfg.GetFeishuWebhookAddr(),
		allowedOpenIDs:    allowedOpenIDs,
		messages:          make(chan *Message, feishuMessageBufferSize),
		done:              make(chan struct{}),
		seen:              make(map[string]time.Time),
	}

	if ch.encryptKey != "" {
		ch.encryptedKey = lark.EncryptKey(ch.encryptKey)
	}
	return ch
}

// Name returns the channel name.
func (f *FeishuChannel) Name() string {
	return "feishu"
}

// Start initializes the Feishu bot and begins listening for webhook events.
func (f *FeishuChannel) Start(ctx context.Context) error {
	bot := lark.NewChatBot(f.appID, f.appSecret)
	if err := bot.StartHeartbeat(); err != nil {
		return fmt.Errorf("feishu heartbeat failed: %w", err)
	}
	f.bot = bot
	logger.Info("feishu bot connected", "appID", f.appID)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/event", f.handleEvent)

	f.server = &http.Server{
		Addr:              f.webhookAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		logger.Info("feishu webhook listening", "addr", f.webhookAddr)
		if err := f.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("feishu webhook server error", "err", err)
		}
	}()

	// Periodic dedup cache cleanup.
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-f.done:
				return
			case <-ticker.C:
				f.cleanupSeen()
			}
		}
	}()

	logger.Info("feishu channel started")
	return nil
}

// Stop gracefully shuts down the channel.
func (f *FeishuChannel) Stop() error {
	f.stopOnce.Do(func() {
		close(f.done)
		if f.bot != nil {
			f.bot.StopHeartbeat()
		}
		if f.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := f.server.Shutdown(ctx); err != nil {
				logger.Error("feishu webhook shutdown error", "err", err)
			}
		}
		f.wg.Wait()
		close(f.messages)
		logger.Info("feishu channel stopped")
	})
	return nil
}

// Send sends a response message via Feishu.
// resp.ReplyTo format: "p2p:{openID}" or "group:{chatID}"
func (f *FeishuChannel) Send(ctx context.Context, resp *Response) error {
	if f.bot == nil {
		return fmt.Errorf("feishu bot not started")
	}

	chunks := SplitMessage(resp.Text, feishuMaxMessageLength)
	for _, chunk := range chunks {
		var uid *lark.OptionalUserID
		replyTo := resp.ReplyTo
		if strings.HasPrefix(replyTo, "p2p:") {
			uid = lark.WithOpenID(strings.TrimPrefix(replyTo, "p2p:"))
		} else if strings.HasPrefix(replyTo, "group:") {
			uid = lark.WithChatID(strings.TrimPrefix(replyTo, "group:"))
		} else {
			// Fallback: treat as open_id.
			uid = lark.WithOpenID(replyTo)
		}

		if _, err := f.bot.PostText(chunk, uid); err != nil {
			return fmt.Errorf("feishu send error: %w", err)
		}
	}
	return nil
}

// Messages returns the incoming message channel.
func (f *FeishuChannel) Messages() <-chan *Message {
	return f.messages
}

// handleEvent processes incoming Feishu webhook events.
func (f *FeishuChannel) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, feishuMaxBodySize))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Decrypt if encrypt key is configured.
	if f.encryptedKey != nil {
		var encrypted lark.EncryptedReq
		if err := json.Unmarshal(body, &encrypted); err == nil && encrypted.Encrypt != "" {
			decrypted, err := lark.Decrypt(f.encryptedKey, encrypted.Encrypt)
			if err != nil {
				logger.Error("feishu decrypt error", "err", err)
				http.Error(w, "decrypt failed", http.StatusBadRequest)
				return
			}
			body = decrypted
		}
	}

	// URL verification challenge.
	var challenge lark.EventChallenge
	if err := json.Unmarshal(body, &challenge); err == nil && challenge.Type == "url_verification" {
		if f.verificationToken != "" && challenge.Token != f.verificationToken {
			http.Error(w, "token mismatch", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"challenge": challenge.Challenge}); err != nil {
			logger.Warn("feishu challenge response write error", "err", err)
		}
		return
	}

	// Parse event v2.
	var event lark.EventV2
	if err := json.Unmarshal(body, &event); err != nil {
		logger.Error("feishu event parse error", "err", err)
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}

	// Verify token.
	if f.verificationToken != "" && event.Header.Token != f.verificationToken {
		http.Error(w, "token mismatch", http.StatusForbidden)
		return
	}

	// Respond 200 immediately (Feishu retries if no response within 3s).
	w.WriteHeader(http.StatusOK)

	// Dedup: skip already-seen events.
	eventID := event.Header.EventID
	if eventID != "" && !f.markSeen(eventID) {
		return
	}

	// Process message event.
	if event.Header.EventType == lark.EventTypeMessageReceived {
		f.processMessageEvent(&event)
	}
}

// feishuTextContent is the JSON structure for text message content.
type feishuTextContent struct {
	Text string `json:"text"`
}

// feishuImageContent is the JSON structure for image message content.
type feishuImageContent struct {
	ImageKey string `json:"image_key"`
}

// feishuFileContent is the JSON structure for file message content.
type feishuFileContent struct {
	FileKey  string `json:"file_key"`
	FileName string `json:"file_name"`
}

// feishuMediaContent is the JSON structure for media (video) message content.
type feishuMediaContent struct {
	FileKey  string `json:"file_key"`
	FileName string `json:"file_name"`
	ImageKey string `json:"image_key"`
	Duration int    `json:"duration"`
}

// feishuAudioContent is the JSON structure for audio message content.
type feishuAudioContent struct {
	FileKey  string `json:"file_key"`
	Duration int    `json:"duration"`
}

// feishuStickerContent is the JSON structure for sticker message content.
type feishuStickerContent struct {
	FileKey string `json:"file_key"`
}

// processMessageEvent extracts a message from a Feishu message event.
func (f *FeishuChannel) processMessageEvent(event *lark.EventV2) {
	received, err := event.GetMessageReceived()
	if err != nil {
		logger.Error("feishu get message received error", "err", err)
		return
	}
	if received.Sender.SenderID.OpenID == "" || received.Message.MessageID == "" {
		logger.Debug("feishu ignoring event with missing sender or message ID")
		return
	}

	var text string
	metadata := map[string]string{}

	switch received.Message.MessageType {
	case "text":
		var content feishuTextContent
		if err := json.Unmarshal([]byte(received.Message.Content), &content); err != nil {
			logger.Error("feishu content parse error", "err", err)
			return
		}
		text = strings.TrimSpace(content.Text)
	case "image":
		var content feishuImageContent
		if err := json.Unmarshal([]byte(received.Message.Content), &content); err != nil {
			logger.Error("feishu image content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("image", "image_key", content.ImageKey)
		text = "[Image received]"
	case "file":
		var content feishuFileContent
		if err := json.Unmarshal([]byte(received.Message.Content), &content); err != nil {
			logger.Error("feishu file content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("file",
			"file_key", content.FileKey, "file_name", content.FileName)
		if content.FileName != "" {
			text = fmt.Sprintf("[File: %s]", content.FileName)
		} else {
			text = "[File received]"
		}
	case "media":
		var content feishuMediaContent
		if err := json.Unmarshal([]byte(received.Message.Content), &content); err != nil {
			logger.Error("feishu media content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("video",
			"file_key", content.FileKey, "file_name", content.FileName,
			"duration", fmtSeconds(content.Duration))
		text = "[Video received]"
	case "audio":
		var content feishuAudioContent
		if err := json.Unmarshal([]byte(received.Message.Content), &content); err != nil {
			logger.Error("feishu audio content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("audio",
			"file_key", content.FileKey, "duration", fmtSeconds(content.Duration))
		text = "[Audio received]"
	case "sticker":
		var content feishuStickerContent
		if err := json.Unmarshal([]byte(received.Message.Content), &content); err != nil {
			logger.Error("feishu sticker content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("sticker", "file_key", content.FileKey)
		text = "[Sticker received]"
	default:
		logger.Debug("feishu ignoring unsupported message type", "type", received.Message.MessageType)
		return
	}

	if text == "" {
		return
	}

	openID := received.Sender.SenderID.OpenID

	// Sender allowlist check.
	if len(f.allowedOpenIDs) > 0 && !f.allowedOpenIDs[openID] {
		logger.Warn("feishu message from unauthorized user", "openID", openID)
		return
	}

	chatID := received.Message.ChatID
	chatType := received.Message.ChatType // "p2p" or "group"

	var replyTarget string
	var channelID string
	if chatType == "group" {
		replyTarget = "group:" + chatID
		channelID = "feishu:group:" + chatID
	} else {
		replyTarget = "p2p:" + openID
		channelID = "feishu:" + openID
	}

	metadata["chat_id"] = replyTarget
	metadata["chat_type"] = chatType
	metadata["message_id"] = received.Message.MessageID

	msg := &Message{
		ID:        received.Message.MessageID,
		ChannelID: channelID,
		UserID:    openID,
		Username:  openID, // Feishu doesn't provide username in events; use openID as fallback.
		Text:      text,
		Metadata:  metadata,
	}

	select {
	case f.messages <- msg:
	case <-f.done:
	default:
		logger.Warn("feishu message buffer full, dropping message")
	}
}

// markSeen returns true if the eventID is new (first time seen), false if duplicate.
func (f *FeishuChannel) markSeen(eventID string) bool {
	f.seenMu.Lock()
	defer f.seenMu.Unlock()
	if _, exists := f.seen[eventID]; exists {
		return false
	}
	f.seen[eventID] = time.Now()
	return true
}

// cleanupSeen removes expired entries from the dedup cache.
func (f *FeishuChannel) cleanupSeen() {
	f.seenMu.Lock()
	defer f.seenMu.Unlock()
	cutoff := time.Now().Add(-feishuDedupTTL)
	for id, t := range f.seen {
		if t.Before(cutoff) {
			delete(f.seen, id)
		}
	}
}
