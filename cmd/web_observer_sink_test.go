package cmd

import (
	"testing"

	"github.com/linanwx/nagobot/session"
)

// TestObservableSession pins which sessions may be mirrored to a browser. The
// exclusions are not cosmetic: mirroring an internal sibling puts a returned
// value ("created pins/foo.md") on the page, and mirroring a web session
// delivers everything twice.
func TestObservableSession(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"discord:1502707848944287895", true},
		{"telegram:12345", true},
		{"wecom:group:abc", true},
		{"cli", true},
		// Cron is kept on purpose — a browser open on it is how you watch a
		// scheduled job run.
		{"cron:tidyup", true},

		// Already a web session: the conversation lives there.
		{"web:a7a8bbb9", false},
		// Internal siblings return values to a caller, they do not speak.
		{"web:abc" + session.QuoteSessionSuffix, false},
		{"discord:123" + session.PinSessionSuffix, false},
		{"telegram:9" + session.ImagePreviewSessionSuffix, false},
		// Children address their parent, which is itself mirrored.
		{"discord:123" + session.ThreadsSessionInfix + "search", false},
		{"cli" + session.ForkSessionInfix + "plan", false},

		{"", false},
	}
	for _, tc := range cases {
		if got := observableSession(tc.key); got != tc.want {
			t.Errorf("observableSession(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// TestWebObserverSinkRequiresWebChannel pins that the mirror is opt-in on
// deployment shape: no web channel configured means no extra sink, so a
// telegram-only bot keeps exactly the delivery it had.
func TestWebObserverSinkRequiresWebChannel(t *testing.T) {
	if _, ok := webObserverSink(nil, "discord:123"); ok {
		t.Fatal("nil channel manager must not yield an observer sink")
	}
}
