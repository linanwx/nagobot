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
	ch := NewWebChannel(cfg, nil).(*WebChannel)
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
	if t2 != 70.0 {
		t.Errorf("tier2_trigger_percent = %v, want 70", t2)
	}

	t3, ok := resp["tier3_trigger_percent"].(float64)
	if !ok {
		t.Fatalf("tier3_trigger_percent missing or not a number: %v", resp["tier3_trigger_percent"])
	}
	if t3 != 85.0 {
		t.Errorf("tier3_trigger_percent = %v, want 85", t3)
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

func TestHandleSessionChat(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "discord:42")
	sessDir := filepath.Join(ch.workspace, sessionsDirName, "discord", "42")
	chatLines := `{"role":"user","content":"[Nansen]: hello","ts":"2026-07-16T10:00:00Z"}` + "\n" +
		`{"role":"assistant","content":"hi there","ts":"2026-07-16T10:00:05Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "chat.jsonl"), []byte(chatLines), 0o644); err != nil {
		t.Fatal(err)
	}

	rw := httptest.NewRecorder()
	ch.handleSessionChat(rw, "discord:42")
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var resp sessionChatResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Key != "discord:42" || len(resp.Messages) != 2 {
		t.Fatalf("key=%q messages=%d, want discord:42 / 2", resp.Key, len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || resp.Messages[0].Content != "[Nansen]: hello" {
		t.Fatalf("unexpected first message: %+v", resp.Messages[0])
	}
	if resp.Messages[1].Ts.IsZero() {
		t.Fatal("assistant ts not parsed")
	}
}

func TestHandleSessionChat_NoChatLog(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "cron:job1")

	rw := httptest.NewRecorder()
	ch.handleSessionChat(rw, "cron:job1")
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
}

func TestHandleSessionChat_RouteSuffix(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "discord:42")
	sessDir := filepath.Join(ch.workspace, sessionsDirName, "discord", "42")
	if err := os.WriteFile(filepath.Join(sessDir, "chat.jsonl"),
		[]byte(`{"role":"assistant","content":"x","ts":"2026-07-16T10:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/discord/42/chat", nil)
	rw := httptest.NewRecorder()
	ch.handleSessionMessages(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), `"messages"`) {
		t.Fatalf("unexpected body: %s", rw.Body.String())
	}
}

func TestHandlePrompts_ListAndRead(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:prompts")
	sysDir := filepath.Join(ch.workspace, "system")
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysDir, "GLOBAL.md"), []byte("# persona"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sysDir, "sections"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysDir, "sections", "tools.md"), []byte("# tools"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Present on disk but NOT whitelisted — must not be listed or readable.
	if err := os.WriteFile(filepath.Join(sysDir, "CORE_MECHANISM.md"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	// List: whitelisted files that exist, with labels, in whitelist order.
	rw := httptest.NewRecorder()
	ch.handlePrompts(rw, httptest.NewRequest(http.MethodGet, "/api/prompts", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var list struct {
		Files []struct{ Name, Label string } `json:"files"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Files) != 2 || list.Files[0].Name != "GLOBAL.md" || list.Files[1].Name != "sections/tools.md" {
		t.Fatalf("expected [GLOBAL.md sections/tools.md], got %+v", list.Files)
	}
	if list.Files[0].Label != "Global persona" {
		t.Fatalf("expected label, got %+v", list.Files[0])
	}

	// Read: content round-trips.
	rw = httptest.NewRecorder()
	ch.handlePromptFile(rw, httptest.NewRequest(http.MethodGet, "/api/prompts/GLOBAL.md", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var file map[string]string
	if err := json.Unmarshal(rw.Body.Bytes(), &file); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if file["content"] != "# persona" {
		t.Fatalf("content = %q", file["content"])
	}
}

func TestHandlePromptFile_RejectsTraversal(t *testing.T) {
	ch := newTestWebChannelWithSession(t, "web:prompts2")
	for _, path := range []string{
		"/api/prompts/../config.yaml",
		"/api/prompts/..%2Fconfig.yaml",
		"/api/prompts/notes.txt",
		"/api/prompts/.hidden.md",
		"/api/prompts/CORE_MECHANISM.md", // on disk but not whitelisted
	} {
		rw := httptest.NewRecorder()
		ch.handlePromptFile(rw, httptest.NewRequest(http.MethodGet, path, nil))
		if rw.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rw.Code)
		}
	}
}

func TestRedactTree_CoversNestedAndMapSecrets(t *testing.T) {
	tree := map[string]any{
		"providers": map[string]any{
			"siliconflowCN": map[string]any{"apiKey": "sk-live", "modelType": "glm"},
			"mimo":          map[string]any{"apiKey": "tp-live"},
			"openaiOAuth":   map[string]any{"accessToken": "at", "refreshToken": "rt", "tokenType": "Bearer"},
		},
		"r2":  map[string]any{"accessKeyId": "AKIA", "secretAccessKey": "shh", "bucket": "media"},
		"env": map[string]any{"HASS_TOKEN": "eyJ", "HA_URL": "http://ha.local"},
		"tools": map[string]any{
			"web": map[string]any{"search": map[string]any{"keys": map[string]any{"google": "g-key"}}},
		},
		"thread": map[string]any{"provider": "deepseek", "modelType": "deepseek-v4-flash"},
	}
	redactTree(tree, false)

	leaks := []string{"sk-live", "tp-live", "at", "rt", "AKIA", "shh", "eyJ", "http://ha.local", "g-key"}
	blob, _ := json.Marshal(tree)
	for _, leak := range leaks {
		if strings.Contains(string(blob), `"`+leak+`"`) {
			t.Errorf("secret %q survived redaction: %s", leak, blob)
		}
	}
	// Non-secret display data stays readable.
	for _, keep := range []string{"deepseek-v4-flash", "media", "glm"} {
		if !strings.Contains(string(blob), keep) {
			t.Errorf("non-secret %q was over-redacted: %s", keep, blob)
		}
	}
}
