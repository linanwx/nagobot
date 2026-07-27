package channel

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// --- Archived sessions: the deployment-wide "hide this from the list" set ---
//
// Archiving is a property of the SESSION, not of the viewer: a session archived
// from one browser is archived for everyone, and the state survives a restart.
// That is why it lives in the workspace (system/archived_sessions.json) next to
// the other cross-viewer web state rather than in localStorage — unlike the
// sidebar's cron/cli/old filters, which are one person's view preferences.
//
// The server only records the flag and reports it on GET /api/sessions; nothing
// here filters. Hiding is the client's job, for the same reason the other
// filters are: the funnel menu has to be able to show archived sessions back
// again without a second round trip, and the "N hidden" count needs the full
// list to be meaningful.

// archiveFileName is the workspace-relative store. Its shape is
// {session key: archived-at}, one entry per archived session; the timestamp is
// not read back by anything, it is there so a human reading the file can tell
// when a row was filed away.
const archiveFileName = "archived_sessions.json"

type archiveRequest struct {
	SessionID string `json:"session_id"`
	// Archived is the state the caller wants. Explicit rather than a toggle:
	// two pages can have stale, disagreeing views of the same session, and a
	// toggle would let the second click undo the first person's intent.
	Archived bool `json:"archived"`
}

func (w *WebChannel) archiveFile() string {
	if w.workspace == "" {
		return ""
	}
	return filepath.Join(w.workspace, "system", archiveFileName)
}

// loadArchived reads the archive set at startup. A missing file is the first
// run; a corrupt one is reported and treated as empty, because refusing to
// serve the session list over an unreadable preference file would be worse than
// showing a few rows that should have been hidden.
func (w *WebChannel) loadArchived() {
	path := w.archiveFile()
	if path == "" {
		return
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return // first run
	}
	var raw map[string]time.Time
	if err := json.Unmarshal(buf, &raw); err != nil {
		logger.Warn("web channel: bad archived sessions file", "path", path, "err", err)
		return
	}
	w.archivedMu.Lock()
	defer w.archivedMu.Unlock()
	for key, at := range raw {
		w.archived[key] = at
	}
}

// setArchived records or clears the flag for one session and persists the whole
// set. Returns an error only when the write failed — the caller must not report
// success for a change that dies with the process.
func (w *WebChannel) setArchived(key string, archived bool) error {
	w.archivedMu.Lock()
	defer w.archivedMu.Unlock()

	if _, exists := w.archived[key]; exists == archived {
		return nil // already in the requested state
	}
	if archived {
		w.archived[key] = time.Now().UTC()
	} else {
		delete(w.archived, key)
	}

	path := w.archiveFile()
	if path == "" {
		return nil
	}
	buf, err := json.MarshalIndent(w.archived, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o600)
}

// archivedKeys snapshots the set for one pass over the session list.
func (w *WebChannel) archivedKeys() map[string]bool {
	w.archivedMu.Lock()
	defer w.archivedMu.Unlock()
	if len(w.archived) == 0 {
		return nil
	}
	out := make(map[string]bool, len(w.archived))
	for key := range w.archived {
		out[key] = true
	}
	return out
}

// handleArchive sets or clears the archived flag of one session.
//
// The session is not required to exist on disk: a key is validated by SHAPE
// only, the same rule the rest of the API applies. A flag for a session that
// was never created (or has since been removed) costs one line in a JSON file
// and matches nothing in the list — cheaper than a stat call that would still
// race with the session being created a moment later.
func (w *WebChannel) handleArchive(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}
	var body archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(rw, "bad request body", http.StatusBadRequest)
		return
	}
	key := sanitizeSessionKey(body.SessionID)
	if key == "" {
		http.Error(rw, "invalid session id", http.StatusBadRequest)
		return
	}
	if err := w.setArchived(key, body.Archived); err != nil {
		logger.Warn("web channel: save archived sessions failed", "err", err)
		http.Error(rw, "failed to save archive state", http.StatusInternalServerError)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"key": key, "archived": body.Archived})
}
