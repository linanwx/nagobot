package channel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestWeCom(t *testing.T) *WeComChannel {
	t.Helper()
	return &WeComChannel{
		mediaDir: t.TempDir(),
		messages: make(chan *Message, 4),
		seen:     make(map[string]time.Time),
	}
}

func deliverWeCom(t *testing.T, w *WeComChannel, body string) *Message {
	t.Helper()
	w.handleMsgCallback(wsFrame{Cmd: wsCmdMsgCallback, Body: json.RawMessage(body)})
	select {
	case msg := <-w.messages:
		return msg
	default:
		t.Fatal("no message delivered")
		return nil
	}
}

// A group @-mention that quotes text used to arrive as the @-line alone: the
// quote object was never parsed, so the bot answered without seeing what it
// was asked about.
func TestWeComQuotedTextReachesTheMessage(t *testing.T) {
	w := newTestWeCom(t)
	msg := deliverWeCom(t, w, `{
		"msgid": "m1", "chattype": "group", "chatid": "wrG1",
		"from": {"userid": "alice"}, "msgtype": "text",
		"text": {"content": "@bot what does this mean?"},
		"quote": {"msgtype": "text", "text": {"content": "line one\nline two"}}
	}`)

	want := "> line one\n> line two\n\n@bot what does this mean?"
	if msg.Text != want {
		t.Fatalf("text = %q, want %q", msg.Text, want)
	}
	if msg.ChannelID != "wecom:group:wrG1" {
		t.Fatalf("channel = %q", msg.ChannelID)
	}
}

// In a group, quoting is the only way an image or file reaches the bot at
// all — WeCom never delivers a bare group image. The quoted media must be
// downloaded and land in media_summary like a directly sent one.
func TestWeComQuotedImageIsDownloaded(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Write(png)
	}))
	defer srv.Close()

	w := newTestWeCom(t)
	msg := deliverWeCom(t, w, `{
		"msgid": "m2", "chattype": "group", "chatid": "wrG1",
		"from": {"userid": "alice"}, "msgtype": "text",
		"text": {"content": "@bot describe this"},
		"quote": {"msgtype": "image", "image": {"url": "`+srv.URL+`"}}
	}`)

	if msg.Text != "> [Image]\n\n@bot describe this" {
		t.Fatalf("text = %q", msg.Text)
	}
	summary := msg.Metadata["media_summary"]
	if !strings.Contains(summary, "image_path: "+w.mediaDir) {
		t.Fatalf("media_summary = %q, want an image_path under %s", summary, w.mediaDir)
	}
}

// A quoted download that fails stays visible in the quote instead of
// vanishing, and adds no media_summary.
func TestWeComQuotedFileDownloadFailureIsVisible(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	w := newTestWeCom(t)
	msg := deliverWeCom(t, w, `{
		"msgid": "m3", "chattype": "group", "chatid": "wrG1",
		"from": {"userid": "alice"}, "msgtype": "text",
		"text": {"content": "@bot summarize"},
		"quote": {"msgtype": "file", "file": {"url": "`+srv.URL+`", "file_name": "a.pdf"}}
	}`)

	if msg.Text != "> [File: download failed]\n\n@bot summarize" {
		t.Fatalf("text = %q", msg.Text)
	}
	if _, ok := msg.Metadata["media_summary"]; ok {
		t.Fatalf("unexpected media_summary %q", msg.Metadata["media_summary"])
	}
}

// Quoted mixed content keeps its text items.
func TestWeComQuotedMixed(t *testing.T) {
	w := newTestWeCom(t)
	msg := deliverWeCom(t, w, `{
		"msgid": "m4", "chattype": "single",
		"from": {"userid": "bob"}, "msgtype": "text",
		"text": {"content": "and this?"},
		"quote": {"msgtype": "mixed", "mixed": {"msg_item": [
			{"msgtype": "text", "text": {"content": "first"}},
			{"msgtype": "text", "text": {"content": "second"}}
		]}}
	}`)

	if msg.Text != "> first\n> second\n\nand this?" {
		t.Fatalf("text = %q", msg.Text)
	}
}

// A message without a quote is unchanged.
func TestWeComPlainTextUnchanged(t *testing.T) {
	w := newTestWeCom(t)
	msg := deliverWeCom(t, w, `{
		"msgid": "m5", "chattype": "single",
		"from": {"userid": "bob"}, "msgtype": "text",
		"text": {"content": "hello"}
	}`)
	if msg.Text != "hello" {
		t.Fatalf("text = %q", msg.Text)
	}
	if msg.ChannelID != "wecom:bob" {
		t.Fatalf("channel = %q", msg.ChannelID)
	}
}
