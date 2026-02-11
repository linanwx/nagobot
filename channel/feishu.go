package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-lark/lark"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const (
	feishuMessageBufferSize = 100
	feishuMaxMessageLength  = 4000
)

// FeishuChannel implements the Channel interface for Feishu (Lark).
type FeishuChannel struct {
	appID, appSecret       string
	verificationToken      string
	encryptKey             string
	webhookAddr            string
	bot                    *lark.Bot
	server                 *http.Server
	messages               chan *Message
	done                   chan struct{}
	wg                     sync.WaitGroup
	msgID                  atomic.Int64
	encryptedKey           []byte // precomputed from encryptKey
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

	ch := &FeishuChannel{
		appID:             appID,
		appSecret:         appSecret,
		verificationToken: cfg.GetFeishuVerificationToken(),
		encryptKey:        cfg.GetFeishuEncryptKey(),
		webhookAddr:       cfg.GetFeishuWebhookAddr(),
		messages:          make(chan *Message, feishuMessageBufferSize),
		done:              make(chan struct{}),
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
		Addr:    f.webhookAddr,
		Handler: mux,
	}

	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		logger.Info("feishu webhook listening", "addr", f.webhookAddr)
		if err := f.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("feishu webhook server error", "err", err)
		}
	}()

	logger.Info("feishu channel started")
	return nil
}

// Stop gracefully shuts down the channel.
func (f *FeishuChannel) Stop() error {
	close(f.done)
	if f.bot != nil {
		f.bot.StopHeartbeat()
	}
	if f.server != nil {
		f.server.Shutdown(context.Background())
	}
	f.wg.Wait()
	close(f.messages)
	logger.Info("feishu channel stopped")
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
			// Fallback: treat as open_id for backward compatibility.
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
	body, err := io.ReadAll(r.Body)
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
		json.NewEncoder(w).Encode(map[string]string{"challenge": challenge.Challenge})
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

	// Respond 200 immediately.
	w.WriteHeader(http.StatusOK)

	// Process message event.
	if event.Header.EventType == lark.EventTypeMessageReceived {
		f.processMessageEvent(event)
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

// processMessageEvent extracts a message from a Feishu message event.
func (f *FeishuChannel) processMessageEvent(event lark.EventV2) {
	received, err := event.GetMessageReceived()
	if err != nil {
		logger.Error("feishu get message received error", "err", err)
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
		metadata["media_type"] = "image"
		metadata["image_key"] = content.ImageKey
		text = "[Image received]"
	case "file":
		var content feishuFileContent
		if err := json.Unmarshal([]byte(received.Message.Content), &content); err != nil {
			logger.Error("feishu file content parse error", "err", err)
			return
		}
		metadata["media_type"] = "file"
		metadata["file_key"] = content.FileKey
		metadata["file_name"] = content.FileName
		text = fmt.Sprintf("[File: %s]", content.FileName)
	case "audio":
		metadata["media_type"] = "audio"
		text = "[Audio received]"
	case "sticker":
		metadata["media_type"] = "sticker"
		text = "[Sticker received]"
	default:
		logger.Debug("feishu ignoring unsupported message type", "type", received.Message.MessageType)
		return
	}

	if text == "" {
		return
	}

	openID := received.Sender.SenderID.OpenID
	chatID := received.Message.ChatID
	chatType := received.Message.ChatType // "p2p" or "group"

	var replyTarget string
	if chatType == "group" {
		replyTarget = "group:" + chatID
	} else {
		replyTarget = "p2p:" + openID
	}

	metadata["chat_id"] = replyTarget
	metadata["chat_type"] = chatType
	metadata["message_id"] = received.Message.MessageID

	n := f.msgID.Add(1)
	msg := &Message{
		ID:        fmt.Sprintf("feishu-%d", n),
		ChannelID: "feishu:" + openID,
		UserID:    openID,
		Text:      text,
		Metadata:  metadata,
	}

	select {
	case f.messages <- msg:
	case <-f.done:
	}
}
