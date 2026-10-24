package channel

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/linanwx/nagobot/auth"
	"github.com/linanwx/nagobot/config"
)

// newSendTestChannel returns a web channel whose auth manager knows one
// person ("nansen") holding a send key bound to discord:123.
func newSendTestChannel(t *testing.T) (*WebChannel, string) {
	t.Helper()
	workspace := t.TempDir()
	systemDir := workspace + "/system"
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed the person through the registry file: the auth package exposes no
	// create outside the login-link flow.
	persons := `{"persons":[{"id":"p_test","username":"nansen","created_at":"2026-10-24T00:00:00Z"}]}`
	if err := os.WriteFile(systemDir+"/persons.json", []byte(persons), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := auth.NewManager(systemDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := mgr.MintSendKey("nansen", "discord:123")
	if err != nil {
		t.Fatal(err)
	}
	ch := NewWebChannel(config.DefaultConfig(), mgr).(*WebChannel)
	ch.workspace = workspace
	return ch, key
}

func postSend(ch *WebChannel, key, contentType string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/send", bytes.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", contentType)
	rw := httptest.NewRecorder()
	ch.handleSend(rw, req)
	return rw
}

func TestSendRejectsMissingOrWrongKey(t *testing.T) {
	ch, key := newSendTestChannel(t)
	if rw := postSend(ch, "", "application/json", []byte(`{"text":"hi"}`)); rw.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status %d", rw.Code)
	}
	if rw := postSend(ch, key+"0", "application/json", []byte(`{"text":"hi"}`)); rw.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status %d", rw.Code)
	}
	if len(ch.messages) != 0 {
		t.Fatal("a rejected request must not queue a message")
	}
}

func TestSendQueuesTextAsThePersonIntoTheBoundSession(t *testing.T) {
	ch, key := newSendTestChannel(t)
	rw := postSend(ch, key, "application/json", []byte(`{"text":"  买牛奶  "}`))
	if rw.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rw.Code, rw.Body.String())
	}
	msg := <-ch.messages
	if msg.ChannelID != "web:discord:123" || msg.Text != "买牛奶" || msg.Username != "nansen" {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Metadata["person_id"] != "p_test" || msg.Metadata[MetaStandingDelivery] == "" {
		t.Fatalf("metadata = %v", msg.Metadata)
	}
}

func TestSendAcceptsFormText(t *testing.T) {
	ch, key := newSendTestChannel(t)
	rw := postSend(ch, key, "application/x-www-form-urlencoded", []byte("text=%E6%B5%8B%E8%AF%95"))
	if rw.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rw.Code, rw.Body.String())
	}
	if msg := <-ch.messages; msg.Text != "测试" {
		t.Fatalf("text = %q", msg.Text)
	}
}

// An Apple Watch shortcut posts its recording as the raw body, labelled
// audio/x-m4a. It must land in media/ as an .m4a with an audio_path summary,
// which is what triggers the dispatcher's upfront transcription.
func TestSendStoresRawAudioBody(t *testing.T) {
	ch, key := newSendTestChannel(t)
	rw := postSend(ch, key, "audio/x-m4a", []byte("fake-m4a-bytes"))
	if rw.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rw.Code, rw.Body.String())
	}
	msg := <-ch.messages
	summary := msg.Metadata["media_summary"]
	if !strings.Contains(summary, "audio_path: ") || !strings.Contains(summary, ".m4a") {
		t.Fatalf("summary = %q", summary)
	}
	if msg.Text != "[Audio received]" {
		t.Fatalf("text = %q", msg.Text)
	}
}

func TestSendRejectsEmptyAndUnsupported(t *testing.T) {
	ch, key := newSendTestChannel(t)
	if rw := postSend(ch, key, "application/json", []byte(`{"text":"   "}`)); rw.Code != http.StatusBadRequest {
		t.Fatalf("empty text: status %d", rw.Code)
	}
	if rw := postSend(ch, key, "video/mp4", []byte("x")); rw.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("video: status %d", rw.Code)
	}
}
