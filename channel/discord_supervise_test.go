package channel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
)

// silentGateway returns an HTTP test server that completes the websocket
// upgrade and then never sends anything. This reproduces the production hang:
// discordgo's Open() blocks forever in the deadline-less ReadMessage waiting
// for the Op 10 Hello packet while holding the session mutex.
func silentGateway(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Never send a frame; hold the connection open.
		<-make(chan struct{})
		_ = conn.Close()
	}))
}

// gatewayTransport redirects discordgo's gateway discovery REST call to the
// fake server URL. discordgo has no public way to preset the gateway address.
type gatewayTransport struct{ url string }

func (rt gatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := `{"url":"` + rt.url + `"}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// hungDiscordSession builds a session whose Open() will establish the
// websocket against the silent gateway and then block forever.
func hungDiscordSession(t *testing.T, gw *httptest.Server) *discordgo.Session {
	t.Helper()
	dg, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	dg.Client = &http.Client{Transport: gatewayTransport{url: "ws://" + gw.Listener.Addr().String()}}
	dg.Dialer = &websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	return dg
}

// Regression test for the 2026-09-15 outage: the gateway accepts the websocket
// but never speaks again. Open() must not be allowed to block forever —
// discordConnect has to time out and return so the supervisor can rebuild.
func TestDiscordConnect_OpenHang_AbandonsInsteadOfBlocking(t *testing.T) {
	gw := silentGateway(t)
	defer gw.Close()
	dg := hungDiscordSession(t, gw)

	start := time.Now()
	err := discordConnect(dg, 500*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from hung Open(), got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("discordConnect returned too slowly: %v", elapsed)
	}
}

// A refused connection must surface quickly as an error, not a timeout.
func TestDiscordConnect_ConnectRefused_ReturnsError(t *testing.T) {
	gw := silentGateway(t)
	gw.Close() // port now refuses connections
	dg := hungDiscordSession(t, gw)

	err := discordConnect(dg, 2*time.Second)
	if err == nil {
		t.Fatal("expected error for refused connection, got nil")
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("refused connection should fail fast, got timeout: %v", err)
	}
}

// The stale-session guard: events arriving from a session that is no longer
// current (an abandoned one whose reconnect loop revived) must be dropped.
func TestDiscordHandler_DropsEventsFromStaleSession(t *testing.T) {
	d := &DiscordChannel{
		messages: make(chan *Message, discordMessageBufferSize),
		done:     make(chan struct{}),
	}
	current, err := discordgo.New("Bot current")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	current.State.User = &discordgo.User{ID: "bot-self"}
	stale, err := discordgo.New("Bot stale")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	stale.State.User = &discordgo.User{ID: "bot-self"}

	d.setSession(current)
	handler := d.sessionHandler()

	ev := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "m1",
		ChannelID: "c1",
		Author:    &discordgo.User{ID: "u1", Username: "alice"},
		Content:   "hello",
	}}

	// Event from the stale session must be dropped.
	handler(stale, ev)
	select {
	case msg := <-d.messages:
		t.Fatalf("stale session event leaked through: %+v", msg)
	default:
	}

	// Event from the current session must flow through.
	handler(current, ev)
	select {
	case msg := <-d.messages:
		if msg.Text != "hello" {
			t.Fatalf("unexpected message text: %q", msg.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("message from current session was not delivered")
	}
}
