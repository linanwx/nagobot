package channel

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

const (
	wecomWSURL             = "wss://openws.work.weixin.qq.com"
	wecomMessageBufferSize = 100
	wecomMaxMessageLength  = 2048
	wecomDedupTTL          = 5 * time.Minute
	wecomHeartbeatInterval = 30 * time.Second
	wecomMaxMissedPong     = 2
	wecomReplyACKTimeout   = 5 * time.Second
	wecomReconnectBase     = 1 * time.Second
	wecomReconnectMaxDelay = 30 * time.Second
	wecomMaxReconnect      = 10
	wecomMaxAuthFailure    = 5
)

// WeCom WebSocket frame commands.
const (
	wsCmdSubscribe       = "aibot_subscribe"
	wsCmdPing            = "ping"
	wsCmdRespondMsg      = "aibot_respond_msg"
	wsCmdMsgCallback     = "aibot_msg_callback"
	wsCmdEventCallback   = "aibot_event_callback"
)

// wsFrame is the unified WeCom WebSocket frame format.
type wsFrame struct {
	Cmd     string            `json:"cmd,omitempty"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body,omitempty"`
	ErrCode int               `json:"errcode,omitempty"`
	ErrMsg  string            `json:"errmsg,omitempty"`
}

// wecomMsgBody represents the body of an aibot_msg_callback frame.
type wecomMsgBody struct {
	MsgID      string `json:"msgid"`
	AIBotID    string `json:"aibotid"`
	ChatType   string `json:"chattype"` // "single" or "group"
	ChatID     string `json:"chatid"`
	MsgType    string `json:"msgtype"`
	CreateTime int64  `json:"create_time"`
	From       struct {
		UserID string `json:"userid"`
	} `json:"from"`
	Text  *struct{ Content string } `json:"text,omitempty"`
	Image *struct {
		URL    string `json:"url"`
		AESKey string `json:"aeskey"`
	} `json:"image,omitempty"`
	Voice *struct{ Content string } `json:"voice,omitempty"`
	File  *struct {
		URL      string `json:"url"`
		AESKey   string `json:"aeskey"`
		FileName string `json:"file_name"`
	} `json:"file,omitempty"`
	Video *struct {
		URL    string `json:"url"`
		AESKey string `json:"aeskey"`
	} `json:"video,omitempty"`
	Mixed *struct {
		MsgItem []struct {
			MsgType string  `json:"msgtype"`
			Text    *struct{ Content string } `json:"text,omitempty"`
			Image   *struct {
				URL    string `json:"url"`
				AESKey string `json:"aeskey"`
			} `json:"image,omitempty"`
		} `json:"msg_item"`
	} `json:"mixed,omitempty"`
	Event *struct {
		EventType string `json:"eventtype"`
	} `json:"event,omitempty"`
}

// pendingACK tracks a reply waiting for server acknowledgement.
type pendingACK struct {
	ch    chan wsFrame
	timer *time.Timer
}

// WeComChannel implements the Channel interface for WeCom (WeChat Work)
// using the AI Bot WebSocket long connection (no public URL needed).
type WeComChannel struct {
	botID, secret  string
	allowedUserIDs map[string]bool
	mediaDir       string

	connMu sync.Mutex
	conn   *websocket.Conn

	messages chan *Message
	done     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once

	// dedup
	seenMu sync.Mutex
	seen   map[string]time.Time

	// heartbeat
	missedPong int

	// reconnect
	reconnectAttempts   int
	authFailureAttempts int
	manualClose         atomic.Bool

	// reply ACK tracking
	ackMu      sync.Mutex
	pendingAck map[string]*pendingACK

	// last req_id per target (userid or "group:chatid") for reply routing
	reqIDMu   sync.Mutex
	lastReqID map[string]reqIDEntry
}

type reqIDEntry struct {
	reqID string
	at    time.Time
}

// NewWeComChannel creates a new WeCom channel from config.
// Returns nil if botId is not configured (same pattern as other channels).
func NewWeComChannel(cfg *config.Config) Channel {
	botID := cfg.GetWeComBotID()
	if botID == "" {
		logger.Warn("WeCom botId not configured, skipping WeCom channel")
		return nil
	}
	allowed := make(map[string]bool)
	for _, id := range cfg.GetWeComAllowedUserIDs() {
		allowed[id] = true
	}
	return &WeComChannel{
		botID:          cfg.GetWeComBotID(),
		secret:         cfg.GetWeComSecret(),
		allowedUserIDs: allowed,
		mediaDir:       initMediaDir(cfg),
		messages:       make(chan *Message, wecomMessageBufferSize),
		done:           make(chan struct{}),
		seen:           make(map[string]time.Time),
		pendingAck:     make(map[string]*pendingACK),
		lastReqID:      make(map[string]reqIDEntry),
	}
}

func (w *WeComChannel) Name() string             { return "wecom" }
func (w *WeComChannel) Messages() <-chan *Message { return w.messages }

func (w *WeComChannel) Start(ctx context.Context) error {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.connectLoop(ctx)
	}()

	// Dedup cache cleanup.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-ticker.C:
				w.cleanupSeen()
				w.cleanupReqIDs()
			}
		}
	}()

	logger.Info("wecom channel started", "botID", w.botID)
	return nil
}

func (w *WeComChannel) Stop() error {
	w.stopOnce.Do(func() {
		w.manualClose.Store(true)
		close(w.done)
		w.connMu.Lock()
		if w.conn != nil {
			w.conn.Close()
		}
		w.connMu.Unlock()
		w.wg.Wait()
		close(w.messages)
		logger.Info("wecom channel stopped")
	})
	return nil
}

// Send sends a text reply via WebSocket.
// resp.ReplyTo is the target (userid or "group:{chatid}"), used to look up the last req_id.
func (w *WeComChannel) Send(ctx context.Context, resp *Response) error {
	target := resp.ReplyTo

	// Look up the req_id for this target.
	w.reqIDMu.Lock()
	reqID := w.lastReqID[target].reqID
	w.reqIDMu.Unlock()

	if reqID == "" {
		return fmt.Errorf("wecom: no req_id for target %q (user may not have sent a message yet)", target)
	}

	chunks := SplitMessage(resp.Text, wecomMaxMessageLength)
	for _, chunk := range chunks {
		body, _ := json.Marshal(map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": chunk},
		})
		frame := wsFrame{
			Cmd:     wsCmdRespondMsg,
			Headers: map[string]string{"req_id": reqID},
			Body:    body,
		}
		if err := w.sendAndWaitACK(frame); err != nil {
			return fmt.Errorf("wecom send: %w", err)
		}
	}
	return nil
}

// connectLoop manages the WebSocket lifecycle: connect → auth → read loop → reconnect.
func (w *WeComChannel) connectLoop(ctx context.Context) {
	for {
		select {
		case <-w.done:
			return
		default:
		}

		if err := w.connectAndRun(ctx); err != nil {
			logger.Warn("wecom connection ended", "err", err)
		}

		if w.manualClose.Load() {
			return
		}

		delay := w.scheduleReconnect()
		if delay < 0 {
			return // max attempts exhausted
		}

		select {
		case <-w.done:
			return
		case <-time.After(delay):
		}
	}
}

func (w *WeComChannel) connectAndRun(ctx context.Context) error {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wecomWSURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	w.connMu.Lock()
	w.conn = conn
	w.missedPong = 0
	w.connMu.Unlock()

	defer func() {
		w.connMu.Lock()
		if w.conn == conn {
			w.conn = nil
		}
		w.connMu.Unlock()
		conn.Close()
		w.clearPendingACKs("connection closed")
	}()

	// Authenticate.
	if err := w.sendAuth(conn); err != nil {
		return fmt.Errorf("auth send: %w", err)
	}

	// Wait for auth response in the read loop.
	authed := make(chan bool, 1)
	readDone := make(chan struct{})
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer close(readDone)
		w.readLoop(ctx, conn, authed)
	}()

	select {
	case ok := <-authed:
		if !ok {
			return fmt.Errorf("authentication failed")
		}
	case <-w.done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("auth timeout")
	}

	// Auth succeeded — reset counters.
	w.reconnectAttempts = 0
	w.authFailureAttempts = 0
	logger.Info("wecom authenticated", "botID", w.botID)

	// Start heartbeat.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.heartbeatLoop(conn)
	}()

	// Block until connection dies, readLoop exits, or shutdown.
	select {
	case <-readDone:
		return fmt.Errorf("connection lost")
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *WeComChannel) sendAuth(conn *websocket.Conn) error {
	body, _ := json.Marshal(map[string]string{
		"bot_id": w.botID,
		"secret": w.secret,
	})
	frame := wsFrame{
		Cmd:     wsCmdSubscribe,
		Headers: map[string]string{"req_id": generateReqID(wsCmdSubscribe)},
		Body:    body,
	}
	return w.writeFrame(conn, frame)
}

func (w *WeComChannel) readLoop(_ context.Context, conn *websocket.Conn, authed chan<- bool) {
	authSent := false
	defer func() {
		if !authSent {
			authed <- false
		}
		// Signal connectAndRun to exit by cancelling done or closing conn.
		w.connMu.Lock()
		if w.conn == conn {
			w.conn = nil
		}
		w.connMu.Unlock()
		conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !w.manualClose.Load() {
				logger.Warn("wecom read error", "err", err)
			}
			return
		}

		var frame wsFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			logger.Warn("wecom: failed to parse frame", "err", err)
			continue
		}

		w.handleFrame(frame, conn, authed, &authSent)
	}
}

func (w *WeComChannel) handleFrame(frame wsFrame, conn *websocket.Conn, authed chan<- bool, authSent *bool) {
	reqID := frame.Headers["req_id"]

	// Message callback.
	if frame.Cmd == wsCmdMsgCallback {
		w.handleMsgCallback(frame)
		return
	}

	// Event callback.
	if frame.Cmd == wsCmdEventCallback {
		w.handleEventCallback(frame, conn)
		return
	}

	// ACK frames (no cmd) — dispatch by req_id prefix.
	if strings.HasPrefix(reqID, wsCmdSubscribe) {
		if frame.ErrCode != 0 {
			logger.Error("wecom auth failed", "errcode", frame.ErrCode, "errmsg", frame.ErrMsg)
			w.authFailureAttempts++
			if !*authSent {
				*authSent = true
				authed <- false
			}
			return
		}
		if !*authSent {
			*authSent = true
			authed <- true
		}
		return
	}

	if strings.HasPrefix(reqID, wsCmdPing) {
		if frame.ErrCode == 0 {
			w.missedPong = 0
		}
		return
	}

	// Reply ACK.
	w.ackMu.Lock()
	if p, ok := w.pendingAck[reqID]; ok {
		p.timer.Stop()
		delete(w.pendingAck, reqID)
		w.ackMu.Unlock()
		p.ch <- frame
		return
	}
	w.ackMu.Unlock()
}

func (w *WeComChannel) handleMsgCallback(frame wsFrame) {
	var body wecomMsgBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		logger.Warn("wecom: failed to parse msg body", "err", err)
		return
	}

	// Dedup by msgid.
	if body.MsgID != "" && !w.markSeen(body.MsgID) {
		return
	}

	// Allowed user check.
	if len(w.allowedUserIDs) > 0 && !w.allowedUserIDs[body.From.UserID] {
		logger.Warn("wecom: message from unauthorized user", "userid", body.From.UserID)
		return
	}

	reqID := frame.Headers["req_id"]
	channelID := "wecom:" + body.From.UserID
	target := body.From.UserID // used for sink routing
	if body.ChatType == "group" && body.ChatID != "" {
		channelID = "wecom:group:" + body.ChatID
		target = "group:" + body.ChatID
	}

	// Store latest req_id for this target so Send() can find it.
	w.reqIDMu.Lock()
	w.lastReqID[target] = reqIDEntry{reqID: reqID, at: time.Now()}
	w.reqIDMu.Unlock()

	msg := &Message{
		ID:        body.MsgID,
		ChannelID: channelID,
		UserID:    body.From.UserID,
		Username:  body.From.UserID,
		Metadata: map[string]string{
			"wecom_req_id": reqID,
			"chat_type":    body.ChatType,
			"chat_id":      target,
		},
	}

	switch body.MsgType {
	case "text":
		if body.Text != nil {
			msg.Text = body.Text.Content
		}
	case "image":
		if body.Image != nil {
			if path := downloadWeComMedia(w.mediaDir, body.Image.URL, body.Image.AESKey); path != "" {
				msg.Metadata["media_summary"] = MediaSummary("photo", "image_path", path)
				msg.Text = "[Image received]"
			} else {
				msg.Text = "[Image: download failed]"
			}
		}
	case "voice":
		if body.Voice != nil {
			msg.Text = body.Voice.Content
		}
	case "file":
		if body.File != nil {
			if path := downloadWeComMedia(w.mediaDir, body.File.URL, body.File.AESKey); path != "" {
				msg.Metadata["media_summary"] = MediaSummary("file",
					"file_name", body.File.FileName,
					"file_path", path,
				)
				msg.Text = "[File: " + body.File.FileName + "]"
			} else {
				msg.Text = "[File: download failed]"
			}
		}
	case "video":
		if body.Video != nil {
			if path := downloadWeComMedia(w.mediaDir, body.Video.URL, body.Video.AESKey); path != "" {
				msg.Metadata["media_summary"] = MediaSummary("video", "file_path", path)
				msg.Text = "[Video received]"
			} else {
				msg.Text = "[Video: download failed]"
			}
		}
	case "mixed":
		if body.Mixed != nil {
			msg.Text = w.handleMixedMsg(body.Mixed.MsgItem, msg.Metadata)
		}
	default:
		msg.Text = fmt.Sprintf("[Unsupported message type: %s]", body.MsgType)
	}

	select {
	case w.messages <- msg:
	default:
		logger.Warn("wecom: message buffer full, dropping message")
	}
}

func (w *WeComChannel) handleMixedMsg(items []struct {
	MsgType string  `json:"msgtype"`
	Text    *struct{ Content string } `json:"text,omitempty"`
	Image   *struct {
		URL    string `json:"url"`
		AESKey string `json:"aeskey"`
	} `json:"image,omitempty"`
}, metadata map[string]string) string {
	var parts []string
	imgIdx := 0
	for _, item := range items {
		switch item.MsgType {
		case "text":
			if item.Text != nil {
				parts = append(parts, item.Text.Content)
			}
		case "image":
			if item.Image != nil {
				if path := downloadWeComMedia(w.mediaDir, item.Image.URL, item.Image.AESKey); path != "" {
					key := "media_summary"
					if imgIdx > 0 {
						key = "media_summary_" + strconv.Itoa(imgIdx)
					}
					metadata[key] = MediaSummary("photo", "image_path", path)
					imgIdx++
					parts = append(parts, "[Image]")
				}
			}
		}
	}
	if len(parts) == 0 {
		return "[Mixed message]"
	}
	return strings.Join(parts, "\n")
}

func (w *WeComChannel) handleEventCallback(frame wsFrame, conn *websocket.Conn) {
	var body wecomMsgBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		return
	}
	if body.Event != nil && body.Event.EventType == "disconnected_event" {
		logger.Warn("wecom: disconnected by server (new connection established)")
		w.manualClose.Store(true)
		w.connMu.Lock()
		if w.conn == conn {
			w.conn = nil
		}
		w.connMu.Unlock()
		conn.Close()
	}
}

// heartbeatLoop sends periodic ping frames.
func (w *WeComChannel) heartbeatLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(wecomHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			if w.missedPong >= wecomMaxMissedPong {
				logger.Warn("wecom: missed pong limit reached, closing connection")
				w.connMu.Lock()
				if w.conn == conn {
					w.conn = nil
				}
				w.connMu.Unlock()
				conn.Close()
				return
			}
			w.missedPong++
			frame := wsFrame{
				Cmd:     wsCmdPing,
				Headers: map[string]string{"req_id": generateReqID(wsCmdPing)},
			}
			if err := w.writeFrame(conn, frame); err != nil {
				logger.Warn("wecom: heartbeat send failed", "err", err)
				return
			}
		}
	}
}

// sendAndWaitACK sends a frame and waits for the server ACK.
func (w *WeComChannel) sendAndWaitACK(frame wsFrame) error {
	reqID := frame.Headers["req_id"]
	ack := &pendingACK{
		ch:    make(chan wsFrame, 1),
		timer: time.NewTimer(wecomReplyACKTimeout),
	}

	w.ackMu.Lock()
	w.pendingAck[reqID] = ack
	w.ackMu.Unlock()

	w.connMu.Lock()
	conn := w.conn
	w.connMu.Unlock()

	if conn == nil {
		w.ackMu.Lock()
		delete(w.pendingAck, reqID)
		w.ackMu.Unlock()
		ack.timer.Stop()
		return fmt.Errorf("not connected")
	}

	if err := w.writeFrame(conn, frame); err != nil {
		w.ackMu.Lock()
		delete(w.pendingAck, reqID)
		w.ackMu.Unlock()
		ack.timer.Stop()
		return err
	}

	select {
	case resp := <-ack.ch:
		if resp.ErrCode != 0 {
			return fmt.Errorf("reply error: %d %s", resp.ErrCode, resp.ErrMsg)
		}
		return nil
	case <-ack.timer.C:
		w.ackMu.Lock()
		delete(w.pendingAck, reqID)
		w.ackMu.Unlock()
		return fmt.Errorf("ACK timeout (%v)", wecomReplyACKTimeout)
	}
}

func (w *WeComChannel) clearPendingACKs(reason string) {
	w.ackMu.Lock()
	defer w.ackMu.Unlock()
	for reqID, p := range w.pendingAck {
		p.timer.Stop()
		// Send zero-value error frame instead of closing (avoids race with sendAndWaitACK select).
		select {
		case p.ch <- wsFrame{ErrCode: -1, ErrMsg: reason}:
		default:
		}
		delete(w.pendingAck, reqID)
	}
}

// scheduleReconnect computes the reconnect delay. Returns -1 if max attempts exhausted.
func (w *WeComChannel) scheduleReconnect() time.Duration {
	if w.authFailureAttempts >= wecomMaxAuthFailure {
		logger.Error("wecom: max auth failure attempts reached", "attempts", w.authFailureAttempts)
		return -1
	}
	if w.reconnectAttempts >= wecomMaxReconnect {
		logger.Error("wecom: max reconnect attempts reached", "attempts", w.reconnectAttempts)
		return -1
	}

	w.reconnectAttempts++
	delay := min(
		time.Duration(float64(wecomReconnectBase)*math.Pow(2, float64(w.reconnectAttempts-1))),
		wecomReconnectMaxDelay,
	)
	logger.Info("wecom: reconnecting", "attempt", w.reconnectAttempts, "delay", delay)
	return delay
}

// writeFrame serializes and sends a frame to the WebSocket connection.
func (w *WeComChannel) writeFrame(conn *websocket.Conn, frame wsFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	w.connMu.Lock()
	defer w.connMu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

// -- Dedup --

func (w *WeComChannel) markSeen(msgID string) bool {
	w.seenMu.Lock()
	defer w.seenMu.Unlock()
	if _, exists := w.seen[msgID]; exists {
		return false
	}
	w.seen[msgID] = time.Now()
	return true
}

func (w *WeComChannel) cleanupSeen() {
	w.seenMu.Lock()
	defer w.seenMu.Unlock()
	cutoff := time.Now().Add(-wecomDedupTTL)
	for id, t := range w.seen {
		if t.Before(cutoff) {
			delete(w.seen, id)
		}
	}
}

const wecomReqIDTTL = 1 * time.Hour

func (w *WeComChannel) cleanupReqIDs() {
	w.reqIDMu.Lock()
	defer w.reqIDMu.Unlock()
	cutoff := time.Now().Add(-wecomReqIDTTL)
	for target, entry := range w.lastReqID {
		if entry.at.Before(cutoff) {
			delete(w.lastReqID, target)
		}
	}
}

// -- Helpers --

func generateReqID(prefix string) string {
	buf := make([]byte, 4)
	rand.Read(buf)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}

// downloadWeComMedia downloads and decrypts an AES-encrypted media file from WeCom.
func downloadWeComMedia(mediaDir, url, aesKey string) string {
	if mediaDir == "" || url == "" {
		return ""
	}

	resp, err := http.Get(url)
	if err != nil {
		logger.Warn("wecom: failed to download media", "err", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("wecom: media download returned non-200", "status", resp.StatusCode)
		return ""
	}

	const maxMediaSize = 20 << 20 // 20 MB
	encrypted, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaSize))
	if err != nil {
		logger.Warn("wecom: failed to read media", "err", err)
		return ""
	}

	var content []byte
	if aesKey != "" {
		content, err = decryptWeComFile(encrypted, aesKey)
		if err != nil {
			logger.Warn("wecom: failed to decrypt media", "err", err)
			return ""
		}
	} else {
		content = encrypted
	}

	// Detect extension from content type or default.
	ext := extensionFromContentType(resp.Header.Get("Content-Type"))
	if ext == "" {
		ext = detectExtFromMagic(content)
	}
	if ext == "" {
		ext = ".dat"
	}

	buf := make([]byte, 4)
	rand.Read(buf)
	fileName := fmt.Sprintf("wecom-%s-%s%s", time.Now().Format("20060102-150405"), hex.EncodeToString(buf), ext)
	filePath := filepath.Join(mediaDir, fileName)

	if err := os.WriteFile(filePath, content, 0644); err != nil {
		logger.Warn("wecom: failed to write media file", "err", err)
		return ""
	}

	return filePath
}

// decryptWeComFile decrypts AES-256-CBC encrypted data with PKCS#7 padding (block size 32).
func decryptWeComFile(encrypted []byte, aesKeyB64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(aesKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decode aes key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid aes key length: %d", len(key))
	}

	iv := key[:16]

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(encrypted)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext not multiple of block size")
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	decrypted := make([]byte, len(encrypted))
	mode.CryptBlocks(decrypted, encrypted)

	// Remove PKCS#7 padding (block size 32, per WeCom spec).
	padLen := int(decrypted[len(decrypted)-1])
	if padLen < 1 || padLen > 32 || padLen > len(decrypted) {
		return nil, fmt.Errorf("invalid PKCS#7 padding: %d", padLen)
	}
	for i := len(decrypted) - padLen; i < len(decrypted); i++ {
		if decrypted[i] != byte(padLen) {
			return nil, fmt.Errorf("PKCS#7 padding bytes mismatch")
		}
	}

	return decrypted[:len(decrypted)-padLen], nil
}

// detectExtFromMagic detects file extension from magic bytes.
func detectExtFromMagic(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	switch {
	case data[0] == 0xFF && data[1] == 0xD8:
		return ".jpg"
	case data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G':
		return ".png"
	case data[0] == 'G' && data[1] == 'I' && data[2] == 'F':
		return ".gif"
	case data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F':
		return ".webp" // could also be wav/avi
	case len(data) >= 8 && string(data[4:8]) == "ftyp":
		return ".mp4"
	case data[0] == '%' && data[1] == 'P' && data[2] == 'D' && data[3] == 'F':
		return ".pdf"
	}
	return ""
}
