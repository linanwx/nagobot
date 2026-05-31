package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/session"
)

func splitKey(key string) []string { return strings.Split(key, ":") }

// newTestWebChannelWithSession sets up a WebChannel with a temp workspace
// containing a single-message session at the given key. contextBudgetFn is
// stubbed to return a 200K window so tier2/tier3 percents are deterministic.
func newTestWebChannelWithSession(t *testing.T, sessionKey string) *WebChannel {
	t.Helper()
	workspace := t.TempDir()

	// Session key uses ":" as a separator; on-disk it maps to a directory tree.
	keyPath := filepath.Join(splitKey(sessionKey)...)
	sessDir := filepath.Join(workspace, sessionsDirName, keyPath)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := `{"role":"user","content":"hi","timestamp":"2026-04-24T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, session.SessionFileName), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	ch := NewWebChannel(cfg).(*WebChannel)
	ch.workspace = workspace
	ch.SetContextBudgetFn(func(key string) (int, int, bool) {
		if key == sessionKey {
			return 200000, 0, true
		}
		return 0, 0, false
	})
	return ch
}

func TestHandleSessionStats_TierTriggerPercents(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")

	rw := httptest.NewRecorder()
	ch.handleSessionStats(rw, "web:test")

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	t2, ok := resp["tier2_trigger_percent"].(float64)
	if !ok {
		t.Fatalf("tier2_trigger_percent missing or not a number: %v", resp["tier2_trigger_percent"])
	}
	if t2 != 64.0 {
		t.Errorf("tier2_trigger_percent = %v, want 64", t2)
	}

	t3, ok := resp["tier3_trigger_percent"].(float64)
	if !ok {
		t.Fatalf("tier3_trigger_percent missing or not a number: %v", resp["tier3_trigger_percent"])
	}
	if t3 != 80.0 {
		t.Errorf("tier3_trigger_percent = %v, want 80", t3)
	}

	if got := resp["context_window_tokens"].(float64); got != 200000 {
		t.Errorf("context_window_tokens = %v, want 200000", got)
	}
}

func TestHandleSessionFiles(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:files")

	sessDir := filepath.Join(ch.workspace, sessionsDirName, "web", "files")
	// Top-level markdown files (the session.jsonl from the harness must be excluded).
	mustWrite(t, filepath.Join(sessDir, "USER.md"), "user prefs")
	mustWrite(t, filepath.Join(sessDir, "dream.md"), "last night's dream")
	mustWrite(t, filepath.Join(sessDir, "heartbeat.md"), "follow-ups")
	// A non-markdown file and a markdown file in a subdir must NOT be listed.
	mustWrite(t, filepath.Join(sessDir, "notes.txt"), "ignored")
	mustWrite(t, filepath.Join(sessDir, "memory", "m1.md"), "ignored subdir")

	rw := httptest.NewRecorder()
	ch.handleSessionFiles(rw, "web:files")
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}

	var resp struct {
		Key   string `json:"key"`
		Files []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Sorted, top-level .md only: dream.md, heartbeat.md, USER.md.
	gotNames := make([]string, len(resp.Files))
	gotContent := map[string]string{}
	for i, f := range resp.Files {
		gotNames[i] = f.Name
		gotContent[f.Name] = f.Content
	}
	// Handler sorts ASCII: uppercase 'U' (0x55) sorts before lowercase 'd'/'h'.
	want := []string{"USER.md", "dream.md", "heartbeat.md"}
	if strings.Join(gotNames, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", gotNames, want)
	}
	if gotContent["dream.md"] != "last night's dream" {
		t.Errorf("dream.md content = %q", gotContent["dream.md"])
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
