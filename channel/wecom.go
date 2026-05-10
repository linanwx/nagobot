package channel

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
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
	wecomDedupTTL          = 5 * time.Minute
	wecomHeartbeatInterval = 30 * time.Second
	wecomMaxMissedPong     = 2
	wecomReconnectBase     = 1 * time.Second
	wecomReconnectMaxDelay = 30 * time.Second
)

// WeCom WebSocket frame commands.
const (
	wsCmdSubscribe          = "aibot_subscribe"
	wsCmdPing               = "ping"
	wsCmdSendMsg            = "aibot_send_msg"
	wsCmdMsgCallback        = "aibot_msg_callback"
	wsCmdEventCallback      = "aibot_event_callback"
	wsCmdUploadMediaInit    = "aibot_upload_media_init"
	wsCmdUploadMediaChunk   = "aibot_upload_media_chunk"
	wsCmdUploadMediaFinish  = "aibot_upload_media_finish"
)

// Media upload constraints (per WeCom AI Bot WebSocket spec).
const (
	wecomImageMaxSize    = 10 << 20  // 10 MB
	wecomFileMaxSize     = 20 << 20  // 20 MB
	wecomChunkRawSize    = 256 << 10 // 256 KB raw per chunk (server limit is 512 KB; stay safe)
	wecomMaxChunks       = 100
	wecomUploadAckWait   = 30 * time.Second
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

	// pending request/response correlation: callers register a chan keyed by
	// req_id and wait for the matching ACK frame (used by image upload flow).
	pendingMu sync.Mutex
	pending   map[string]chan wsFrame
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
		botID:          botID,
		secret:         cfg.GetWeComSecret(),
		allowedUserIDs: allowed,
		mediaDir:       initMediaDir(cfg),
		messages:       make(chan *Message, wecomMessageBufferSize),
		done:           make(chan struct{}),
		seen:           make(map[string]time.Time),
		pending:        make(map[string]chan wsFrame),
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

// Send pushes a message via aibot_send_msg (active push).
// resp.ReplyTo encodes the target: bare userid for single chat, "group:{chatid}" for group.
// No 24h reply window: works any time after the user has messaged the bot at least once.
func (w *WeComChannel) Send(ctx context.Context, resp *Response) error {
	target := resp.ReplyTo
	if target == "" {
		return fmt.Errorf("wecom: empty ReplyTo")
	}

	chatID := target
	chatType := 1
	if rest, ok := strings.CutPrefix(target, "group:"); ok {
		chatID = rest
		chatType = 2
	}

	// aibot_send_msg supports markdown / template_card. Plain text renders fine as markdown.
	// WeCom's markdown renderer tries to fetch ![](url) and shows a broken-image
	// placeholder when the URL is unreachable (e.g. local file paths). The image
	// itself is delivered separately via SendImage, so strip image markdown from
	// the text bubble to avoid the broken placeholder.
	content := stripMarkdownImages(resp.Text)
	body, _ := json.Marshal(map[string]any{
		"chatid":    chatID,
		"chat_type": chatType,
		"msgtype":   "markdown",
		"markdown":  map[string]any{"content": content},
	})
	frame := wsFrame{
		Cmd:     wsCmdSendMsg,
		Headers: map[string]string{"req_id": generateReqID("send")},
		Body:    body,
	}

	w.connMu.Lock()
	conn := w.conn
	w.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("wecom: not connected")
	}
	return w.writeFrame(conn, frame)
}

// SendImage uploads an image via the 3-step aibot_upload_media_* flow and
// then pushes it via aibot_send_msg. ImageRef.Path must point to an image
// file ≤10 MB; larger files are rejected before upload.
func (w *WeComChannel) SendImage(ctx context.Context, replyTo string, ref ImageRef) error {
	if replyTo == "" {
		return fmt.Errorf("wecom: empty replyTo")
	}

	chatID := replyTo
	chatType := 1
	if rest, ok := strings.CutPrefix(replyTo, "group:"); ok {
		chatID = rest
		chatType = 2
	}

	data, err := os.ReadFile(ref.Path)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	if len(data) < 5 {
		return fmt.Errorf("image too small: %d bytes", len(data))
	}
	if len(data) > wecomImageMaxSize {
		return fmt.Errorf("image too large: %d bytes (max %d)", len(data), wecomImageMaxSize)
	}

	totalChunks := (len(data) + wecomChunkRawSize - 1) / wecomChunkRawSize
	if totalChunks > wecomMaxChunks {
		return fmt.Errorf("image needs %d chunks (max %d)", totalChunks, wecomMaxChunks)
	}

	w.connMu.Lock()
	conn := w.conn
	w.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("wecom: not connected")
	}

	mediaID, err := w.uploadMedia(conn, "image", filepath.Base(ref.Path), data, totalChunks)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{
		"chatid":    chatID,
		"chat_type": chatType,
		"msgtype":   "image",
		"image":     map[string]any{"media_id": mediaID},
	})
	frame := wsFrame{
		Cmd:     wsCmdSendMsg,
		Headers: map[string]string{"req_id": generateReqID("send")},
		Body:    body,
	}
	return w.writeFrame(conn, frame)
}

// SendDoc uploads a file via the 3-step aibot_upload_media_* flow (type=file)
// and pushes it via aibot_send_msg with msgtype=file. File ≤20 MB.
func (w *WeComChannel) SendDoc(ctx context.Context, replyTo string, ref DocRef) error {
	if replyTo == "" {
		return fmt.Errorf("wecom: empty replyTo")
	}

	chatID := replyTo
	chatType := 1
	if rest, ok := strings.CutPrefix(replyTo, "group:"); ok {
		chatID = rest
		chatType = 2
	}

	data, err := os.ReadFile(ref.Path)
	if err != nil {
		return fmt.Errorf("read doc: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("doc is empty")
	}
	if len(data) > wecomFileMaxSize {
		return fmt.Errorf("doc too large: %d bytes (max %d)", len(data), wecomFileMaxSize)
	}

	totalChunks := (len(data) + wecomChunkRawSize - 1) / wecomChunkRawSize
	if totalChunks > wecomMaxChunks {
		return fmt.Errorf("doc needs %d chunks (max %d)", totalChunks, wecomMaxChunks)
	}

	w.connMu.Lock()
	conn := w.conn
	w.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("wecom: not connected")
	}

	filename := ref.Name
	if filename == "" {
		filename = filepath.Base(ref.Path)
	}

	mediaID, err := w.uploadMedia(conn, "file", filename, data, totalChunks)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{
		"chatid":    chatID,
		"chat_type": chatType,
		"msgtype":   "file",
		"file":      map[string]any{"media_id": mediaID},
	})
	frame := wsFrame{
		Cmd:     wsCmdSendMsg,
		Headers: map[string]string{"req_id": generateReqID("send")},
		Body:    body,
	}
	return w.writeFrame(conn, frame)
}

// Compile-time check: WeComChannel implements DocSender.
var _ DocSender = (*WeComChannel)(nil)

// uploadMedia runs the init → chunk(s) → finish round-trip and returns the media_id.
// mediaType is "image" or "file" per the WeCom AI Bot upload spec.
func (w *WeComChannel) uploadMedia(conn *websocket.Conn, mediaType, filename string, data []byte, totalChunks int) (string, error) {
	hash := md5.Sum(data)
	initBody, _ := json.Marshal(map[string]any{
		"type":         mediaType,
		"filename":     filename,
		"total_size":   len(data),
		"total_chunks": totalChunks,
		"md5":          hex.EncodeToString(hash[:]),
	})
	initAck, err := w.writeAndWait(conn, wsFrame{
		Cmd:     wsCmdUploadMediaInit,
		Headers: map[string]string{"req_id": generateReqID("upinit")},
		Body:    initBody,
	}, wecomUploadAckWait)
	if err != nil {
		return "", fmt.Errorf("upload init: %w", err)
	}
	var initResp struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(initAck.Body, &initResp); err != nil || initResp.UploadID == "" {
		return "", fmt.Errorf("upload init: bad response (body=%s)", string(initAck.Body))
	}

	for i := range totalChunks {
		start := i * wecomChunkRawSize
		end := min(start+wecomChunkRawSize, len(data))
		chunkBody, _ := json.Marshal(map[string]any{
			"upload_id":   initResp.UploadID,
			"chunk_index": i,
			"base64_data": base64.StdEncoding.EncodeToString(data[start:end]),
		})
		if _, err := w.writeAndWait(conn, wsFrame{
			Cmd:     wsCmdUploadMediaChunk,
			Headers: map[string]string{"req_id": generateReqID("upchunk")},
			Body:    chunkBody,
		}, wecomUploadAckWait); err != nil {
			return "", fmt.Errorf("upload chunk %d: %w", i, err)
		}
	}

	finishBody, _ := json.Marshal(map[string]any{"upload_id": initResp.UploadID})
	finishAck, err := w.writeAndWait(conn, wsFrame{
		Cmd:     wsCmdUploadMediaFinish,
		Headers: map[string]string{"req_id": generateReqID("upfin")},
		Body:    finishBody,
	}, wecomUploadAckWait)
	if err != nil {
		return "", fmt.Errorf("upload finish: %w", err)
	}
	var finishResp struct {
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(finishAck.Body, &finishResp); err != nil || finishResp.MediaID == "" {
		return "", fmt.Errorf("upload finish: missing media_id (body=%s)", string(finishAck.Body))
	}
	return finishResp.MediaID, nil
}

// writeAndWait sends a frame and blocks until the ACK with the same req_id
// arrives via handleFrame, or the timeout elapses. Non-zero ErrCode in the ACK
// returns an error.
func (w *WeComChannel) writeAndWait(conn *websocket.Conn, frame wsFrame, timeout time.Duration) (wsFrame, error) {
	reqID := frame.Headers["req_id"]
	ch := make(chan wsFrame, 1)
	w.pendingMu.Lock()
	w.pending[reqID] = ch
	w.pendingMu.Unlock()
	defer func() {
		w.pendingMu.Lock()
		delete(w.pending, reqID)
		w.pendingMu.Unlock()
	}()

	if err := w.writeFrame(conn, frame); err != nil {
		return wsFrame{}, err
	}
	select {
	case ack := <-ch:
		if ack.ErrCode != 0 {
			return ack, fmt.Errorf("errcode=%d errmsg=%s", ack.ErrCode, ack.ErrMsg)
		}
		return ack, nil
	case <-time.After(timeout):
		return wsFrame{}, fmt.Errorf("ack timeout for req_id %s", reqID)
	case <-w.done:
		return wsFrame{}, fmt.Errorf("channel closed")
	}
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

	// Synchronous request waiter (image upload flow): hand the ACK to the
	// caller blocked on this req_id.
	w.pendingMu.Lock()
	ch, waiting := w.pending[reqID]
	w.pendingMu.Unlock()
	if waiting {
		select {
		case ch <- frame:
		default:
		}
		return
	}

	// Unhandled ACK: surface server-side rejections so failed pushes don't go silent.
	if frame.ErrCode != 0 {
		logger.Warn("wecom send rejected", "req_id", reqID, "errcode", frame.ErrCode, "errmsg", frame.ErrMsg)
	}
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

	channelID := "wecom:" + body.From.UserID
	target := body.From.UserID // used for sink routing
	if body.ChatType == "group" && body.ChatID != "" {
		channelID = "wecom:group:" + body.ChatID
		target = "group:" + body.ChatID
	}

	msg := &Message{
		ID:        body.MsgID,
		ChannelID: channelID,
		UserID:    body.From.UserID,
		Username:  body.From.UserID,
		Metadata: map[string]string{
			"chat_type": body.ChatType,
			"chat_id":   target,
		},
	}

	switch body.MsgType {
	case "text":
		if body.Text != nil {
			msg.Text = body.Text.Content
		}
	case "image":
		if body.Image != nil {
			if path := downloadWeComMedia(w.mediaDir, body.Image.URL, body.Image.AESKey, ""); path != "" {
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
			if path := downloadWeComMedia(w.mediaDir, body.File.URL, body.File.AESKey, body.File.FileName); path != "" {
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
			if path := downloadWeComMedia(w.mediaDir, body.Video.URL, body.Video.AESKey, ""); path != "" {
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
	var summaries []string
	for _, item := range items {
		switch item.MsgType {
		case "text":
			if item.Text != nil {
				parts = append(parts, item.Text.Content)
			}
		case "image":
			if item.Image != nil {
				if path := downloadWeComMedia(w.mediaDir, item.Image.URL, item.Image.AESKey, ""); path != "" {
					summaries = append(summaries, MediaSummary("photo", "image_path", path))
					parts = append(parts, "[Image]")
				}
			}
		}
	}
	if len(summaries) > 0 {
		metadata["media_summary"] = strings.Join(summaries, "\n\n")
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

// scheduleReconnect computes the reconnect delay with exponential backoff.
// Never gives up — caps at wecomReconnectMaxDelay after max attempts.
func (w *WeComChannel) scheduleReconnect() time.Duration {
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

// -- Helpers --

func generateReqID(prefix string) string {
	buf := make([]byte, 4)
	rand.Read(buf)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}

// downloadWeComMedia downloads and decrypts an AES-encrypted media file from WeCom.
// originalFileName, when non-empty, takes precedence for extension detection — this
// is critical for office documents (docx/xlsx/pptx) whose magic bytes (PK zip) are
// indistinguishable and whose Content-Type from the WeCom CDN is often opaque.
func downloadWeComMedia(mediaDir, url, aesKey, originalFileName string) string {
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

	// Detect extension: original filename (authoritative for user uploads) →
	// Content-Type → magic bytes → fallback.
	ext := ""
	if originalFileName != "" {
		ext = strings.ToLower(filepath.Ext(originalFileName))
	}
	if ext == "" {
		ext = extensionFromContentType(resp.Header.Get("Content-Type"))
	}
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
	// WeCom aibot sends aeskey as 43-char unpadded standard base64 (e.g. "AdCyZx...0ak").
	// StdEncoding rejects it at byte 40 (where "=" padding is expected).
	// Strip any trailing "=" defensively and use RawStdEncoding.
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(aesKeyB64, "="))
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
