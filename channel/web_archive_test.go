package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func archiveVia(t *testing.T, ch *WebChannel, sessionKey string, archived bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(archiveRequest{SessionID: sessionKey, Archived: archived})
	rw := httptest.NewRecorder()
	ch.handleArchive(rw, httptest.NewRequest(http.MethodPost, "/api/archive", strings.NewReader(string(body))))
	return rw
}

func listSessionsVia(t *testing.T, ch *WebChannel) []sessionListEntry {
	t.Helper()
	rw := httptest.NewRecorder()
	ch.handleSessions(rw, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var entries []sessionListEntry
	if err := json.Unmarshal(rw.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rw.Body.String())
	}
	return entries
}

// The whole feature in one pass: archiving flags the session in the list every
// viewer reads, and unarchiving clears it. The session itself is never touched
// — an archived session is still listed, because the client needs the row to be
// able to show it again.
func TestArchiveRoundTrip(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")

	entries := listSessionsVia(t, ch)
	if len(entries) != 1 || entries[0].Archived {
		t.Fatalf("fresh session should not be archived: %+v", entries)
	}

	if rw := archiveVia(t, ch, "web:test", true); rw.Code != http.StatusOK {
		t.Fatalf("archive status = %d; body=%s", rw.Code, rw.Body.String())
	}
	entries = listSessionsVia(t, ch)
	if len(entries) != 1 || !entries[0].Archived {
		t.Fatalf("archived session should still be listed with archived=true: %+v", entries)
	}

	if rw := archiveVia(t, ch, "web:test", false); rw.Code != http.StatusOK {
		t.Fatalf("unarchive status = %d; body=%s", rw.Code, rw.Body.String())
	}
	entries = listSessionsVia(t, ch)
	if len(entries) != 1 || entries[0].Archived {
		t.Fatalf("unarchived session should report archived=false: %+v", entries)
	}
}

// Archiving is deployment-wide state, not a per-browser preference, so it has
// to outlive the process that recorded it.
func TestArchiveSurvivesRestart(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	if rw := archiveVia(t, ch, "web:test", true); rw.Code != http.StatusOK {
		t.Fatalf("archive status = %d; body=%s", rw.Code, rw.Body.String())
	}

	path := filepath.Join(ch.workspace, "system", archiveFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive file not written: %v", err)
	}

	// A fresh channel over the same workspace — what a daemon restart sees.
	reloaded := &WebChannel{workspace: ch.workspace, archived: map[string]time.Time{}}
	reloaded.loadArchived()
	if !reloaded.archivedKeys()["web:test"] {
		t.Fatalf("archive state lost across restart: %+v", reloaded.archived)
	}
}

// A key is validated by shape, and a malformed one must not reach the store.
func TestArchiveRejectsInvalidKey(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	rw := archiveVia(t, ch, "../../etc/passwd", true)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rw.Code, rw.Body.String())
	}
	if len(ch.archivedKeys()) != 0 {
		t.Fatalf("invalid key was stored: %+v", ch.archived)
	}
}

// A corrupt store must not take the session list down with it: the list is the
// app's entry point, and a few rows that should have been hidden is a far
// smaller failure than no rows at all.
func TestCorruptArchiveFileDegradesToEmpty(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	dir := filepath.Join(ch.workspace, "system")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, archiveFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch.loadArchived()

	entries := listSessionsVia(t, ch)
	if len(entries) != 1 || entries[0].Archived {
		t.Fatalf("corrupt store should degrade to no archived sessions: %+v", entries)
	}
}
