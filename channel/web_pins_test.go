package channel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePin drops a pin file into the session's pins directory.
func writePin(t *testing.T, ch *WebChannel, sessionKey, name, content string) string {
	t.Helper()
	dir := filepath.Join(append([]string{ch.workspace, sessionsDirName}, splitKey(sessionKey)...)...)
	dir = filepath.Join(dir, pinsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func listPinsVia(t *testing.T, ch *WebChannel, sessionKey string) []pinEntry {
	t.Helper()
	rw := httptest.NewRecorder()
	ch.handlePins(rw, httptest.NewRequest(http.MethodGet, "/api/pins?session_id="+sessionKey, nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var resp struct {
		Pins []pinEntry `json:"pins"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rw.Body.String())
	}
	return resp.Pins
}

// The list is what the panel polls, so its two jobs are pinned here: every pin
// gets a usable label (frontmatter title, or the file name when there is none),
// and a session that has never been pinned into is an empty collection rather
// than an error.
func TestListPins_TitleFallsBackToFileName(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	writePin(t, ch, "web:test", "with-frontmatter.md",
		"---\ntitle: Postgres pool settings\nsummary: 'The staging pool: 20 conns'\n---\n\nbody\n")
	writePin(t, ch, "web:test", "hand-written.md", "just a note, no frontmatter\n")
	writePin(t, ch, "web:test", "notes.txt", "not a pin\n")

	pins := listPinsVia(t, ch, "web:test")
	if len(pins) != 2 {
		t.Fatalf("got %d pins, want 2 (the .txt is not a pin): %+v", len(pins), pins)
	}

	byName := map[string]pinEntry{}
	for _, p := range pins {
		byName[p.Name] = p
	}
	if got := byName["with-frontmatter.md"].Title; got != "Postgres pool settings" {
		t.Errorf("title = %q, want the frontmatter title", got)
	}
	if got := byName["with-frontmatter.md"].Summary; got != "The staging pool: 20 conns" {
		t.Errorf("summary = %q, want the quoted scalar unwrapped", got)
	}
	// A pin with no parsable title still has to be clickable.
	if got := byName["hand-written.md"].Title; got != "hand-written" {
		t.Errorf("title = %q, want the file name without its extension", got)
	}
}

func TestListPins_MissingDirectoryIsEmptyNotAnError(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	if pins := listPinsVia(t, ch, "web:test"); len(pins) != 0 {
		t.Fatalf("got %d pins, want none", len(pins))
	}
}

func TestReadAndDeletePin(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	path := writePin(t, ch, "web:test", "note.md", "---\ntitle: Note\n---\n\nthe body\n")

	rw := httptest.NewRecorder()
	ch.handlePins(rw, httptest.NewRequest(http.MethodGet, "/api/pins?session_id=web:test&name=note.md", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var detail pinDetail
	if err := json.Unmarshal(rw.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The whole file comes back, frontmatter included — the client decides what
	// to render, the server does not pre-chew it.
	if !strings.Contains(detail.Content, "title: Note") || !strings.Contains(detail.Content, "the body") {
		t.Errorf("content = %q, want the file verbatim", detail.Content)
	}

	rw = httptest.NewRecorder()
	ch.handlePins(rw, httptest.NewRequest(http.MethodDelete, "/api/pins?session_id=web:test&name=note.md", nil))
	if rw.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rw.Code, rw.Body.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("pin still on disk after delete: %v", err)
	}

	// Deleting again is the state the caller asked for, not a failure — a
	// double-click must not look like an error.
	rw = httptest.NewRecorder()
	ch.handlePins(rw, httptest.NewRequest(http.MethodDelete, "/api/pins?session_id=web:test&name=note.md", nil))
	if rw.Code != http.StatusNoContent {
		t.Errorf("second delete status = %d, want 204", rw.Code)
	}
}

// The name is the only caller-controlled path component, so it must never carry
// path structure. These are the shapes that would otherwise reach outside the
// session's own pins directory.
func TestPinName_RejectsEscapes(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	writePin(t, ch, "web:test", "ok.md", "---\ntitle: ok\n---\n")
	// A file one level up, inside the session directory itself.
	secret := filepath.Join(ch.workspace, sessionsDirName, "web", "test", "heartbeat.md")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Percent-encoded forms are not listed: the mux decodes the query before the
	// handler sees it, so "..%2Fheartbeat.md" arrives as "../heartbeat.md" and
	// is the first case. A doubly-encoded one arrives as a literal file name
	// with no path structure, which is exactly what it should be treated as.
	for _, name := range []string{
		"../heartbeat.md",
		"sub/other.md",
		"ok.txt",
		"..",
	} {
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodDelete} {
				rw := httptest.NewRecorder()
				req := httptest.NewRequest(method, "/api/pins", nil)
				q := req.URL.Query()
				q.Set("session_id", "web:test")
				q.Set("name", name)
				req.URL.RawQuery = q.Encode()
				ch.handlePins(rw, req)
				if rw.Code != http.StatusBadRequest {
					t.Errorf("%s status = %d, want 400", method, rw.Code)
				}
			}
		})
	}
	if _, err := os.Stat(secret); err != nil {
		t.Errorf("file outside pins/ was touched: %v", err)
	}
}

// Pinning answers 202, not 200: the file does not exist yet when this returns.
func TestHandlePin_QueuesAndAcknowledges(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:test")
	var gotKey, gotText string
	ch.SetPinFn(func(key, text string) error {
		gotKey, gotText = key, text
		return nil
	})

	rw := httptest.NewRecorder()
	ch.handlePin(rw, httptest.NewRequest(http.MethodPost, "/api/pin",
		strings.NewReader(`{"session_id":"web:test","text":"| Plan | Price |\n|---|---|"}`)))

	if rw.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rw.Code, rw.Body.String())
	}
	if gotKey != "web:test" {
		t.Errorf("session key = %q, want web:test", gotKey)
	}
	if !strings.Contains(gotText, "| Plan | Price |") {
		t.Errorf("filer got %q, want the markdown unaltered", gotText)
	}
}

func TestHandlePin_Rejections(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   string
		noPin  bool // leave pinFn unset
		fail   bool // pinFn returns an error
		want   int
	}{
		{name: "GET", method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "no filer configured", method: http.MethodPost,
			body: `{"session_id":"web:test","text":"hi"}`, noPin: true, want: http.StatusServiceUnavailable},
		{name: "malformed body", method: http.MethodPost, body: `{`, want: http.StatusBadRequest},
		{name: "blank text", method: http.MethodPost,
			body: `{"session_id":"web:test","text":"  "}`, want: http.StatusBadRequest},
		{name: "unusable session id", method: http.MethodPost,
			body: `{"session_id":"web:../etc","text":"hi"}`, want: http.StatusBadRequest},
		{name: "filer refuses", method: http.MethodPost,
			body: `{"session_id":"web:test","text":"hi"}`, fail: true, want: http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestWebChannelWithSession(t, "web:test")
			if !tc.noPin {
				ch.SetPinFn(func(string, string) error {
					if tc.fail {
						return errors.New("pin filing is not configured")
					}
					return nil
				})
			}
			rw := httptest.NewRecorder()
			ch.handlePin(rw, httptest.NewRequest(tc.method, "/api/pin", strings.NewReader(tc.body)))
			if rw.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rw.Code, tc.want, rw.Body.String())
			}
		})
	}
}
