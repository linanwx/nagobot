package channel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
)

// etagWorkspace lays down one session on disk and returns the channel serving
// it plus the path to its file, so a test can rewrite the file the way
// compression does and ask again.
func etagWorkspace(t *testing.T, key string, msgs []provider.Message) (*WebChannel, string) {
	t.Helper()
	ch := &WebChannel{workspace: t.TempDir()}
	path := ch.resolveSessionFile(key, session.SessionFileName)
	if path == "" {
		t.Fatalf("resolveSessionFile(%q) returned empty", key)
	}
	if err := session.WriteFile(path, &session.Session{Key: key, Messages: msgs}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return ch, path
}

// readSession issues one GET, optionally conditional.
func readSession(t *testing.T, ch *WebChannel, key, view, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/sessions/" + key
	if view != "" {
		url += "?view=" + view
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rw := httptest.NewRecorder()
	ch.handleSessionMessages(rw, req)
	return rw
}

func etagFixture() []provider.Message {
	now := time.Now().UTC()
	return []provider.Message{
		{Role: "user", Content: "how do I rotate the key", Source: "web", Timestamp: now},
		{Role: "assistant", Content: "run nagobot auth login", Timestamp: now},
	}
}

// The whole feature in one pass: a read carries a validator, and handing that
// validator back gets a 304 with no body.
func TestSessionReadIsConditional(t *testing.T) {
	ch, _ := etagWorkspace(t, "web:abc", etagFixture())

	first := readSession(t, ch, "web:abc", "chat", "")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first read — nothing can be revalidated")
	}
	// no-cache, not a TTL: any window of max-age is a window in which a
	// compression pass rewrites the session under a reader told not to ask.
	// private, because a shared cache would otherwise revalidate one person's
	// conversation with another person's cookie and be told 304.
	if got := first.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("Cache-Control = %q, want %q", got, "private, no-cache")
	}

	second := readSession(t, ch, "web:abc", "chat", etag)
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304; body=%s", second.Code, second.Body.String())
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 carried a %d-byte body", second.Body.Len())
	}
	if second.Header().Get("ETag") != etag {
		t.Fatalf("304 ETag = %q, want %q", second.Header().Get("ETag"), etag)
	}
}

// The case the whole design rests on. Tier-1 compression rewrites message
// content IN PLACE: it adds no line and touches no message's timestamp, so
// message_count and updated_at — the two fields /api/sessions already carries,
// and the obvious tempting validator — both come back identical while what the
// reader sees has changed. The file's identity must catch it anyway.
func TestSessionETagSeesAnInPlaceRewrite(t *testing.T) {
	msgs := etagFixture()
	ch, path := etagWorkspace(t, "web:abc", msgs)

	before := readSession(t, ch, "web:abc", "chat", "")
	etag := before.Header().Get("ETag")

	// Exactly what Tier-1 does: a flag and a replacement body on an entry that
	// stays where it is, keeping its own timestamp.
	rewritten := append([]provider.Message(nil), msgs...)
	rewritten[1].Compressed = "[tool result compressed]"
	// mtime is only as precise as the filesystem, and the write below can land
	// inside the same tick as the first one.
	time.Sleep(10 * time.Millisecond)
	if err := session.WriteFile(path, &session.Session{Key: "web:abc", Messages: rewritten}); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	after := readSession(t, ch, "web:abc", "chat", etag)
	if after.Code != http.StatusNotModified {
		return // the validator moved, which is the point
	}
	t.Fatalf("a Tier-1 in-place rewrite was answered 304 — the reader keeps the pre-compression text forever (etag %q)", etag)
}

// One file renders two different bodies, so one validator cannot cover both.
func TestSessionETagSeparatesViews(t *testing.T) {
	ch, _ := etagWorkspace(t, "web:abc", etagFixture())

	full := readSession(t, ch, "web:abc", "", "").Header().Get("ETag")
	chat := readSession(t, ch, "web:abc", "chat", "").Header().Get("ETag")
	if full == chat {
		t.Fatalf("both views share the ETag %q — one would serve the other's body", full)
	}

	crossed := readSession(t, ch, "web:abc", "chat", full)
	if crossed.Code == http.StatusNotModified {
		t.Fatal("the full view's validator satisfied a chat read")
	}
}

// A new message must invalidate, which is the ordinary case.
func TestSessionETagMovesOnAppend(t *testing.T) {
	msgs := etagFixture()
	ch, path := etagWorkspace(t, "web:abc", msgs)
	etag := readSession(t, ch, "web:abc", "chat", "").Header().Get("ETag")

	grown := append(append([]provider.Message(nil), msgs...),
		provider.Message{Role: "user", Content: "thanks", Source: "web", Timestamp: time.Now().UTC()})
	if err := session.WriteFile(path, &session.Session{Key: "web:abc", Messages: grown}); err != nil {
		t.Fatalf("append: %v", err)
	}

	after := readSession(t, ch, "web:abc", "chat", etag)
	if after.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an appended message was not delivered", after.Code)
	}
}

// A missing session must still 404. The stat fails, the conditional path is
// skipped, and ReadFile keeps ownership of the wording.
func TestConditionalPathLeavesMissingSessionsTo404(t *testing.T) {
	ch := &WebChannel{workspace: t.TempDir()}
	rw := readSession(t, ch, "web:nope", "chat", `"chat-1-1"`)
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rw.Code, rw.Body.String())
	}
}

// A view viewMessages would reject must not be answerable from a validator:
// that turns a typo into a silent 304, which is the failure the switch's
// default case exists to prevent.
func TestUnknownViewIsNeverAnsweredFromCache(t *testing.T) {
	ch, _ := etagWorkspace(t, "web:abc", etagFixture())
	rw := readSession(t, ch, "web:abc", "chatt", `"chatt-1-1"`)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rw.Code, rw.Body.String())
	}
	if rw.Header().Get("ETag") != "" {
		t.Fatalf("a rejected view was handed a validator: %q", rw.Header().Get("ETag"))
	}
}

// knownView gates the conditional path and viewMessages renders it; the two
// lists live in different functions and must agree. Asserted as the property —
// knownView is true exactly when viewMessages accepts — rather than by
// restating the list a third time.
func TestKnownViewAgreesWithViewMessages(t *testing.T) {
	for _, view := range []string{"", "chat", "CHAT", "chatt", "full", "raw", " chat", "chat "} {
		_, err := viewMessages(view, nil)
		if got, want := knownView(view), err == nil; got != want {
			t.Errorf("knownView(%q) = %v, but viewMessages accepts = %v", view, got, want)
		}
	}
}

// The unit tests above call the handler directly, which skips the one piece of
// middleware sitting between its ETag and the browser. This runs a real server
// through the real wrapper, with the Accept-Encoding every browser sends: the
// tag must survive compression unchanged, and the revalidation that follows
// must come back as an empty 304. gzhttp's SuffixETag would break exactly this
// and nothing else — see newGzipWrapper.
func TestConditionalReadSurvivesTheGzipWrapper(t *testing.T) {
	msgs := etagFixture()
	// Enough bulk that gzhttp actually engages rather than passing a short body
	// through untouched.
	for range 200 {
		msgs = append(msgs, provider.Message{
			Role:      "assistant",
			Content:   "a reply long enough to be worth compressing, repeated until the body clears the wrapper's minimum size",
			Timestamp: time.Now().UTC(),
		})
	}
	ch, _ := etagWorkspace(t, "web:abc", msgs)

	gzip, err := newGzipWrapper()
	if err != nil {
		t.Fatalf("newGzipWrapper: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions/", ch.handleSessionMessages)
	srv := httptest.NewServer(gzip(mux))
	defer srv.Close()

	get := func(ifNoneMatch string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/sessions/web/abc?view=chat", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		// Set by hand: net/http's transparent gzip only kicks in when the
		// caller does NOT set this, and it strips the header off the response.
		req.Header.Set("Accept-Encoding", "gzip")
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		return resp
	}

	first := get("")
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", first.StatusCode)
	}
	if enc := first.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip — this test is not exercising the wrapper", enc)
	}
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("the wrapper dropped the ETag; nothing can revalidate")
	}

	second := get(etag)
	defer second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 — the tag the wrapper handed out did not match on the way back", second.StatusCode)
	}
	body, _ := io.ReadAll(second.Body)
	if len(body) != 0 {
		t.Fatalf("304 carried a %d-byte body", len(body))
	}
}

func TestETagMatches(t *testing.T) {
	const tag = `"chat-12-34"`
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{tag, true},
		{" " + tag + " ", true},
		{`"other", ` + tag, true},
		{`"other"`, false},
		{"*", true},
		// We only ever issue strong tags, so a weak form came from somewhere
		// else and must not suppress a body we did not produce.
		{`W/` + tag, false},
		// The suffix gzhttp would add under SuffixETag, which is deliberately
		// not configured. If that ever changes, this must be revisited rather
		// than silently start missing on every gzipped response.
		{`"chat-12-34-gzip"`, false},
	}
	for _, c := range cases {
		if got := etagMatches(c.header, tag); got != c.want {
			t.Errorf("etagMatches(%q, %q) = %v, want %v", c.header, tag, got, c.want)
		}
	}
}
