package channel

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// --- Pins: the per-session collection of curated notes ---
//
// A pin is one markdown file in {sessionDir}/pins, written by the pin agent
// (see thread.Manager.Pin). The web client offers two things: a Pin button on a
// message (POST /api/pin, which only ENQUEUES the filing — the agentic turn
// that writes the file runs afterwards), and a panel that lists, reads and
// deletes what has been filed (GET/DELETE /api/pins).
//
// The asymmetry is deliberate: writing goes through the LLM because deciding
// whether new material belongs in an existing pin is a judgement call; reading
// and deleting are plain file operations and never touch a model.

// pinFileExt is the only extension the pins directory serves. Anything else in
// there — a stray directory, a scratch file — is not a pin and is ignored
// rather than reported, so a hand-edited workspace cannot break the panel.
const pinFileExt = ".md"

// pinsDirName is the per-session directory the pin agent writes into. It
// mirrors thread.pinsDirName — the two ends of the same convention, one
// writing and one serving.
const pinsDirName = "pins"

// pinSummaryCap bounds a summary carried in the list response. Summaries are
// meant to be one line; this is the backstop against an agent that wrote an
// essay into the frontmatter.
const pinSummaryCap = 400

type pinRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

// pinEntry is one row of the pins list. Content is carried only by the
// single-pin read: the panel polls the list every few seconds while it is open,
// and shipping every pin's full body on each poll would make that expensive for
// no gain.
type pinEntry struct {
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	Summary  string    `json:"summary,omitempty"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type pinDetail struct {
	pinEntry
	Content string `json:"content"`
}

// SetPinFn sets the pin filer behind POST /api/pin: given a session key and the
// text of the message being pinned, it queues the filing and returns. The
// channel treats it as an opaque request — it never learns what a pin file
// looks like — so replacing the filer is a one-line change in serve.go.
func (w *WebChannel) SetPinFn(fn func(string, string) error) {
	w.pinFn = fn
}

// handlePin queues a message for filing into the session's pins directory.
//
// It answers 202, not 200: the write happens in an agentic turn that runs after
// this returns, so claiming the pin exists would be a lie. The client shows a
// "queued" acknowledgement and the panel picks the file up on a later poll.
func (w *WebChannel) handlePin(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.pinFn == nil {
		http.Error(rw, "pin filing unavailable", http.StatusServiceUnavailable)
		return
	}
	var body pinRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(rw, "bad request body", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		http.Error(rw, "missing text", http.StatusBadRequest)
		return
	}
	key := sanitizeSessionKey(body.SessionID)
	if key == "" {
		http.Error(rw, "invalid session id", http.StatusBadRequest)
		return
	}
	if err := w.pinFn(key, text); err != nil {
		http.Error(rw, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(rw, http.StatusAccepted, map[string]string{"status": "queued"})
}

// handlePins serves the pins of one session: GET without `name` lists them,
// GET with `name` returns one pin's markdown, DELETE with `name` removes it.
func (w *WebChannel) handlePins(rw http.ResponseWriter, r *http.Request) {
	if w.workspace == "" {
		http.Error(rw, "workspace is not configured", http.StatusInternalServerError)
		return
	}
	key := sanitizeSessionKey(r.URL.Query().Get("session_id"))
	if key == "" {
		http.Error(rw, "invalid session id", http.StatusBadRequest)
		return
	}
	dir := w.resolveSessionFile(key, pinsDirName)
	if dir == "" {
		http.Error(rw, "invalid session id", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))

	switch r.Method {
	case http.MethodGet:
		if name == "" {
			w.listPins(rw, dir)
			return
		}
		w.readPin(rw, dir, name)
	case http.MethodDelete:
		w.deletePin(rw, dir, name)
	default:
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (w *WebChannel) listPins(rw http.ResponseWriter, dir string) {
	items := []pinEntry{}
	// A session that has never been pinned into has no directory. That is an
	// empty collection, not an error — the panel shows its empty state.
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(rw, http.StatusOK, map[string]any{"pins": items})
			return
		}
		http.Error(rw, "failed to read pins", http.StatusInternalServerError)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), pinFileExt) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		items = append(items, pinEntryFrom(e.Name(), info, string(raw)))
	}
	// Newest first: a pin is a bookmark of something that just happened, so the
	// thing just pinned should be at the top when the panel opens.
	sort.Slice(items, func(i, j int) bool {
		return items[i].Modified.After(items[j].Modified)
	})
	writeJSON(rw, http.StatusOK, map[string]any{"pins": items})
}

func (w *WebChannel) readPin(rw http.ResponseWriter, dir, name string) {
	path, ok := pinPath(dir, name)
	if !ok {
		http.Error(rw, "invalid pin name", http.StatusBadRequest)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		http.Error(rw, "pin not found", http.StatusNotFound)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		http.Error(rw, "pin not found", http.StatusNotFound)
		return
	}
	writeJSON(rw, http.StatusOK, pinDetail{
		pinEntry: pinEntryFrom(name, info, string(raw)),
		Content:  string(raw),
	})
}

func (w *WebChannel) deletePin(rw http.ResponseWriter, dir, name string) {
	path, ok := pinPath(dir, name)
	if !ok {
		http.Error(rw, "invalid pin name", http.StatusBadRequest)
		return
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			// Already gone is the state the caller asked for. Reporting 404
			// would make a double-click look like a failure.
			rw.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(rw, "failed to delete pin", http.StatusInternalServerError)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// pinPath resolves a pin file name inside dir. The name must be a bare
// basename ending in .md — no separators, no "..". Validating the SHAPE rather
// than resolving-then-containing is enough here because the only legal name has
// no path structure at all, and it keeps the rejection legible to the client.
func pinPath(dir, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", false
	}
	if strings.ContainsAny(name, `/\`) || !strings.EqualFold(filepath.Ext(name), pinFileExt) {
		return "", false
	}
	return filepath.Join(dir, name), true
}

// pinEntryFrom builds a list row from a pin file. Title and summary come from
// the YAML frontmatter the pin agent writes; a file with no parsable title
// falls back to its own name, so a hand-written or half-written pin still shows
// up as something clickable instead of a blank row.
func pinEntryFrom(name string, info os.FileInfo, content string) pinEntry {
	fm := parsePinFrontmatter(content)
	title := strings.TrimSpace(fm["title"])
	if title == "" {
		title = strings.TrimSuffix(name, filepath.Ext(name))
	}
	summary := strings.TrimSpace(fm["summary"])
	if len(summary) > pinSummaryCap {
		summary = summary[:pinSummaryCap]
	}
	return pinEntry{
		Name:     name,
		Title:    title,
		Summary:  summary,
		Size:     info.Size(),
		Modified: info.ModTime(),
	}
}

// parsePinFrontmatter pulls the top-level scalar keys out of a leading YAML
// block. Deliberately minimal: pins carry `title` and `summary` and nothing
// else, so a real YAML parse would buy nothing, and a malformed block degrades
// to "no fields" (→ filename fallback) instead of an error.
func parsePinFrontmatter(content string) map[string]string {
	out := map[string]string{}
	if !strings.HasPrefix(content, "---") {
		return out
	}
	rest := content[3:]
	rest = strings.TrimPrefix(rest, "\r")
	if !strings.HasPrefix(rest, "\n") {
		return out
	}
	rest = rest[1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return out
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // a nested/continuation line — not a top-level key
		}
		k, v, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		out[strings.TrimSpace(k)] = unquoteYAMLScalar(strings.TrimSpace(v))
	}
	return out
}

// unquoteYAMLScalar strips the surrounding quotes a YAML emitter adds around a
// value containing ":" or a leading special character.
func unquoteYAMLScalar(v string) string {
	if len(v) < 2 {
		return v
	}
	if (strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`)) ||
		(strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'")) {
		inner := v[1 : len(v)-1]
		if strings.HasPrefix(v, "'") {
			return strings.ReplaceAll(inner, "''", "'")
		}
		return strings.ReplaceAll(inner, `\"`, `"`)
	}
	return v
}
