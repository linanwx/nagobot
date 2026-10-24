package channel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/linanwx/nagobot/logger"
)

// MetaStandingDelivery marks a message whose reply belongs to the target
// session's own destinations, not to the transport that carried it in. Set by
// POST /api/send: the request came from a device that is not watching any
// page, so the dispatcher must not give the turn a web delivery sink. On a
// discord: session that sink would push a second notification next to
// Discord's, and on a session with no web participants it would broadcast a
// push to every enrolled device.
const MetaStandingDelivery = "standing_delivery"

const maxSendTextRunes = 8000

// --- POST /api/send ---
//
// The entry for devices that cannot hold a browser login, such as an Apple
// Watch shortcut. Authenticated by a send key (Authorization: Bearer nsk_...),
// which names both the person speaking and the one session it may speak into.
// The body is either JSON {"text": "..."}, a form field text=..., or a raw
// media file (a shortcut's "File" request body; Shortcuts form fields can only
// carry strings). Answers 202 as soon as the message is queued. The reply
// arrives through the session's own channel (Discord, web push), never in this
// response, because a watch shortcut cannot wait out an agentic turn.
//
// Deliberately outside protected(): exempt CIDRs and device cookies do not
// apply. A send key is the only credential this endpoint accepts.
func (w *WebChannel) handleSend(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		http.Error(rw, "send key required", http.StatusUnauthorized)
		return
	}
	person, sessionID, ok := w.authMgr.ResolveSendKey(raw)
	if !ok {
		http.Error(rw, "invalid send key", http.StatusUnauthorized)
		return
	}
	if sanitizeSessionKey(sessionID) == "" {
		// The key was minted against a validated session; a bad value here
		// means persons.json was edited by hand. Say so instead of routing.
		logger.Warn("send key bound to an invalid session", "person", person.ID, "session", sessionID)
		http.Error(rw, "send key is bound to an invalid session", http.StatusInternalServerError)
		return
	}

	metadata := map[string]string{
		"chat_id":            sessionID,
		"person_id":          person.ID,
		MetaStandingDelivery: "1",
	}

	contentType := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0])
	var text string
	switch contentType {
	case "application/json":
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(rw, "invalid JSON body", http.StatusBadRequest)
			return
		}
		text = req.Text
	case "application/x-www-form-urlencoded":
		r.Body = http.MaxBytesReader(rw, r.Body, 1<<20)
		text = r.FormValue("text")
	default:
		summary, err := w.saveSendMedia(rw, r, contentType)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errUnsupportedFileType) {
				status = http.StatusUnsupportedMediaType
			}
			http.Error(rw, "upload failed: "+err.Error(), status)
			return
		}
		metadata["media_summary"] = summary
		// A recording has no words of its own; the transcription rides the
		// wake's media_preview. Same placeholder Telegram uses for audio.
		text = "[Audio received]"
		if !strings.Contains(summary, "audio_path:") {
			text = "[File received]"
		}
	}

	text = strings.TrimSpace(text)
	if text == "" {
		http.Error(rw, "empty message", http.StatusBadRequest)
		return
	}
	text = truncateRunes(text, maxSendTextRunes)

	msg := &Message{
		ID:        fmt.Sprintf("web-%d", atomic.AddInt64(&w.msgID, 1)),
		ChannelID: "web:" + sessionID,
		UserID:    sessionID,
		Username:  person.Username,
		Text:      text,
		Metadata:  metadata,
	}
	select {
	case w.messages <- msg:
	case <-w.done:
		http.Error(rw, "shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	}
	logger.Info("send api message queued", "person", person.Username, "session", sessionID, "media", metadata["media_summary"] != "")
	writeJSON(rw, http.StatusAccepted, map[string]string{"status": "queued", "session": sessionID})
}

// saveSendMedia stores a raw-body upload under {workspace}/media and returns
// its media summary.
func (w *WebChannel) saveSendMedia(rw http.ResponseWriter, r *http.Request, contentType string) (string, error) {
	if w.workspace == "" {
		return "", fmt.Errorf("workspace unavailable")
	}
	mediaDir := filepath.Join(w.workspace, "media")
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return "", fmt.Errorf("prepare media directory: %w", err)
	}
	name, err := saveMediaFile(mediaDir, "", contentType, http.MaxBytesReader(rw, r.Body, 20<<20))
	if err != nil {
		return "", err
	}
	summary, _ := webMediaSummary(w.workspace, name, "")
	return summary, nil
}
