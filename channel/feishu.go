package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const (
	feishuMessageBufferSize = 100
	feishuMaxMessageLength  = 4000
	feishuDedupTTL          = 5 * time.Minute
)

// FeishuChannel implements the Channel interface for Feishu (Lark)
// using the official SDK's WebSocket long connection (no public URL needed).
type FeishuChannel struct {
	appID, appSecret string
	allowedOpenIDs   map[string]bool // nil or empty = allow all

	apiClient *lark.Client   // REST client for sending messages
	wsClient  *larkws.Client // WebSocket client for receiving events

	messages chan *Message
	done     chan struct{}
	wg       sync.WaitGroup

	// Event dedup: Feishu may deliver duplicate events.
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

	return &FeishuChannel{
		appID:          appID,
		appSecret:      appSecret,
		allowedOpenIDs: allowedOpenIDs,
		messages:       make(chan *Message, feishuMessageBufferSize),
		done:           make(chan struct{}),
		seen:           make(map[string]time.Time),
	}
}

// Name returns the channel name.
func (f *FeishuChannel) Name() string {
	return "feishu"
}

// Start initializes the Feishu WebSocket long connection and begins receiving events.
func (f *FeishuChannel) Start(ctx context.Context) error {
	// REST client for sending messages.
	f.apiClient = lark.NewClient(f.appID, f.appSecret)

	// Event dispatcher — register message receive handler.
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			f.processMessageEvent(event)
			return nil
		})

	// WebSocket client — SDK handles reconnection internally.
	f.wsClient = larkws.NewClient(f.appID, f.appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelDebug),
	)

	// WebSocket connection goroutine — Start() blocks with select{}.
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		if err := f.wsClient.Start(ctx); err != nil {
			select {
			case <-f.done:
				// Normal shutdown — ignore error.
			default:
				logger.Error("feishu websocket error", "err", err)
			}
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
		// wsClient.Start() blocks on select{} — it will exit when ctx is cancelled
		// by the parent context (serve shutdown). No explicit disconnect needed.
		f.wg.Wait()
		close(f.messages)
		logger.Info("feishu channel stopped")
	})
	return nil
}

// Send sends a response message via Feishu REST API.
// resp.ReplyTo format: "p2p:{openID}" or "group:{chatID}"
func (f *FeishuChannel) Send(ctx context.Context, resp *Response) error {
	if f.apiClient == nil {
		return fmt.Errorf("feishu api client not started")
	}

	chunks := SplitMessage(resp.Text, feishuMaxMessageLength)
	for _, chunk := range chunks {
		var receiveIDType, receiveID string
		replyTo := resp.ReplyTo
		if strings.HasPrefix(replyTo, "p2p:") {
			receiveIDType = "open_id"
			receiveID = strings.TrimPrefix(replyTo, "p2p:")
		} else if strings.HasPrefix(replyTo, "group:") {
			receiveIDType = "chat_id"
			receiveID = strings.TrimPrefix(replyTo, "group:")
		} else {
			// Fallback: treat as open_id.
			receiveIDType = "open_id"
			receiveID = replyTo
		}

		content, _ := json.Marshal(map[string]string{"text": chunk})
		req := larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(receiveIDType).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(receiveID).
				MsgType("text").
				Content(string(content)).
				Build()).
			Build()

		result, err := f.apiClient.Im.Message.Create(ctx, req)
		if err != nil {
			logger.Error("feishu send error", "err", err, "receiveIDType", receiveIDType, "receiveID", receiveID)
			return fmt.Errorf("feishu send error: %w", err)
		}
		if !result.Success() {
			logger.Error("feishu send failed", "code", result.Code, "msg", result.Msg, "receiveIDType", receiveIDType, "receiveID", receiveID)
			return fmt.Errorf("feishu send failed: code=%d msg=%s", result.Code, result.Msg)
		}
		logger.Info("feishu message sent", "receiveIDType", receiveIDType, "receiveID", receiveID)
	}
	return nil
}

// Messages returns the incoming message channel.
func (f *FeishuChannel) Messages() <-chan *Message {
	return f.messages
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

// processMessageEvent extracts a message from a Feishu P2MessageReceiveV1 event.
func (f *FeishuChannel) processMessageEvent(event *larkim.P2MessageReceiveV1) {
	if event.Event == nil || event.Event.Sender == nil || event.Event.Message == nil {
		logger.Debug("feishu ignoring event with missing sender or message")
		return
	}

	sender := event.Event.Sender
	msg := event.Event.Message

	openID := derefStr(sender.SenderId.OpenId)
	messageID := derefStr(msg.MessageId)
	if openID == "" || messageID == "" {
		logger.Debug("feishu ignoring event with missing sender or message ID")
		return
	}

	// Event dedup.
	eventID := ""
	if event.EventV2Base != nil && event.EventV2Base.Header != nil {
		eventID = event.EventV2Base.Header.EventID
	}
	if eventID != "" && !f.markSeen(eventID) {
		return
	}

	msgType := derefStr(msg.MessageType)
	content := derefStr(msg.Content)

	var text string
	metadata := map[string]string{}

	switch msgType {
	case "text":
		var c feishuTextContent
		if err := json.Unmarshal([]byte(content), &c); err != nil {
			logger.Error("feishu content parse error", "err", err)
			return
		}
		text = strings.TrimSpace(c.Text)
	case "image":
		var c feishuImageContent
		if err := json.Unmarshal([]byte(content), &c); err != nil {
			logger.Error("feishu image content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("image", "image_key", c.ImageKey)
		text = "[Image received]"
	case "file":
		var c feishuFileContent
		if err := json.Unmarshal([]byte(content), &c); err != nil {
			logger.Error("feishu file content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("file",
			"file_key", c.FileKey, "file_name", c.FileName)
		if c.FileName != "" {
			text = fmt.Sprintf("[File: %s]", c.FileName)
		} else {
			text = "[File received]"
		}
	case "media":
		var c feishuMediaContent
		if err := json.Unmarshal([]byte(content), &c); err != nil {
			logger.Error("feishu media content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("video",
			"file_key", c.FileKey, "file_name", c.FileName,
			"duration", fmtSeconds(c.Duration))
		text = "[Video received]"
	case "audio":
		var c feishuAudioContent
		if err := json.Unmarshal([]byte(content), &c); err != nil {
			logger.Error("feishu audio content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("audio",
			"file_key", c.FileKey, "duration", fmtSeconds(c.Duration))
		text = "[Audio received]"
	case "sticker":
		var c feishuStickerContent
		if err := json.Unmarshal([]byte(content), &c); err != nil {
			logger.Error("feishu sticker content parse error", "err", err)
			return
		}
		metadata["media_summary"] = MediaSummary("sticker", "file_key", c.FileKey)
		text = "[Sticker received]"
	default:
		logger.Debug("feishu ignoring unsupported message type", "type", msgType)
		return
	}

	if text == "" {
		return
	}

	// Sender allowlist check.
	if len(f.allowedOpenIDs) > 0 && !f.allowedOpenIDs[openID] {
		logger.Warn("feishu message from unauthorized user", "openID", openID)
		return
	}

	chatID := derefStr(msg.ChatId)
	chatType := derefStr(msg.ChatType) // "p2p" or "group"

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
	metadata["message_id"] = messageID

	m := &Message{
		ID:        messageID,
		ChannelID: channelID,
		UserID:    openID,
		Username:  openID, // Feishu doesn't provide username in events; use openID as fallback.
		Text:      text,
		Metadata:  metadata,
	}

	select {
	case f.messages <- m:
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

// derefStr safely dereferences a *string pointer.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
