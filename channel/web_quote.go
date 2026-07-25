package channel

import (
	"encoding/json"
	"net/http"
	"strings"
)

// --- POST /api/quote {session_id, text} → {quote} ---
//
// Turns the text of a message being replied to into ONE line of markdown quote
// (leading "> " included) for the composer's quote preview. The generator is
// injected via SetQuoteFn; this handler only carries text in and text out.
//
// There is deliberately no fallback. If the generator is unconfigured or fails,
// the client gets the error and shows it — a mechanically mangled quote would be
// worse than none, and a silent failure would look like a hung button.

type quoteRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type quoteResponse struct {
	Quote string `json:"quote"`
}

func (w *WebChannel) handleQuote(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.quoteFn == nil {
		http.Error(rw, "quote generation unavailable", http.StatusServiceUnavailable)
		return
	}
	var body quoteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(rw, "bad request body", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		http.Error(rw, "missing text", http.StatusBadRequest)
		return
	}
	// The sibling that generates the quote is keyed off the session, so an
	// unusable session_id is a client bug worth reporting rather than silently
	// remapping to the main session.
	key := sanitizeSessionKey(body.SessionID)
	if key == "" {
		http.Error(rw, "invalid session id", http.StatusBadRequest)
		return
	}

	quote, err := w.quoteFn(r.Context(), key, text)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(rw, http.StatusOK, quoteResponse{Quote: quote})
}
