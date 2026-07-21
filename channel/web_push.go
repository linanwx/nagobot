package channel

import (
	"encoding/json"
	"net/http"

	"github.com/linanwx/nagobot/push"
)

// truncateRunes clips s to at most n runes, appending an ellipsis when cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// --- GET /api/push/vapid-key ---
// Protected: the VAPID public key the browser needs for pushManager.subscribe.
func (w *WebChannel) handlePushKey(rw http.ResponseWriter, r *http.Request) {
	if w.pushMgr == nil {
		http.Error(rw, "push unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]string{"key": w.pushMgr.PublicKey()})
}

// pushSubscribeBody mirrors PushSubscription.toJSON() from the browser.
type pushSubscribeBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// --- POST /api/push/subscribe ---
// Protected: enroll this browser's push subscription, attributed to the
// logged-in person (empty on exempt-IP requests).
func (w *WebChannel) handlePushSubscribe(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.pushMgr == nil {
		http.Error(rw, "push unavailable", http.StatusServiceUnavailable)
		return
	}
	var body pushSubscribeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(rw, "bad request body", http.StatusBadRequest)
		return
	}
	personID := ""
	if person, _ := w.authorize(r); person != nil {
		personID = person.ID
	}
	err := w.pushMgr.Subscribe(push.Subscription{
		PersonID:  personID,
		Endpoint:  body.Endpoint,
		P256dh:    body.Keys.P256dh,
		Auth:      body.Keys.Auth,
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}

// --- POST /api/push/unsubscribe {endpoint} ---
func (w *WebChannel) handlePushUnsubscribe(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.pushMgr == nil {
		http.Error(rw, "push unavailable", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		http.Error(rw, "missing endpoint", http.StatusBadRequest)
		return
	}
	if err := w.pushMgr.Unsubscribe(body.Endpoint); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]bool{"ok": true})
}
