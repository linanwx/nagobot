package thread

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/session"
)

// chanSink builds a labelled destination for a named channel. Channel is the
// identity Union deduplicates on, so tests that care about dedup must set it.
func chanSink(channelName, label string) SessionSink {
	return SessionSink{
		Channel: channelName,
		Label:   label,
		Send:    func(context.Context, string) error { return nil },
	}
}

// TestResolveTurnSinks pins the rule that a session's output always reaches the
// channel it lives on, whoever prompted the turn: the sink for the channel a
// wake ARRIVED on is unioned over the session's standing destinations rather
// than replacing them.
//
// Replacement was the old behavior, and it made the console one-directional —
// a page opened on a Discord group could watch it, but answering from that page
// reached only the page, while the Discord users it was addressed to heard
// nothing.
func TestResolveTurnSinks(t *testing.T) {
	// A Discord session as buildDefaultSinkFor resolves it: the channel it lives
	// on, plus the mirror for any browser watching.
	discordSession := NewSinks(
		chanSink("discord", "discord channel 123"),
		chanSink("web", ""), // the observer contributes no label, by design
	)

	cases := []struct {
		name     string
		source   WakeSource
		wake     SinkSet
		defaults SinkSet
		want     []string // Channel of each destination, in order
	}{
		{
			// The inbound sink wins the collision: it carries this turn's
			// chat.jsonl buffer and Flush, which the standing one does not.
			name: "a discord message on a discord session does not answer twice",
			source: WakeDiscord, defaults: discordSession,
			wake: NewSinks(chanSink("discord", "via discord")),
			want: []string{"discord", "web"},
		},
		{
			name: "a wecom message on a discord session answers on all three",
			source: WakeWeCom, defaults: discordSession,
			wake: NewSinks(chanSink("wecom", "via wecom")),
			want: []string{"wecom", "discord", "web"},
		},
		{
			// The mirror is deduplicated away, so the page is not written twice.
			name: "a web message on a discord session reaches discord too",
			source: WakeWeb, defaults: discordSession,
			wake: NewSinks(chanSink("web", "via web")),
			want: []string{"web", "discord"},
		},
		{
			// No inbound channel at all: cron/peer/progress speak on the
			// session's standing destinations.
			name: "a wake with no origin channel uses the session's own",
			source: WakeCron, defaults: discordSession,
			wake: SinkSet{},
			want: []string{"discord", "web"},
		},
		// Maintenance gets an empty set and keeps it. This is the whole
		// mechanism: there is nowhere to deliver, so nothing is delivered — no
		// downstream check has to remember to suppress it, and no fallback
		// refills the set.
		{"heartbeat gets nothing", WakeHeartbeat, SinkSet{}, discordSession, nil},
		{"legacy heartbeat source gets nothing", WakeSource("heartbeat_reflect"), SinkSet{}, discordSession, nil},
		{"compression gets nothing", WakeCompression, SinkSet{}, discordSession, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTurnSinks(tc.source, tc.wake, tc.defaults)
			if got.Len() != len(tc.want) {
				t.Fatalf("got %d destinations (%q), want %v", got.Len(), got.Label(), tc.want)
			}
			for i, ch := range tc.want {
				if got.ChannelAt(i) != ch {
					t.Fatalf("destination %d = %q, want %q", i, got.ChannelAt(i), ch)
				}
			}
		})
	}
}

// TestResolveTurnSinksSuppressesChunksForSystemWakes pins that a system-initiated
// turn delivers only its final message to a channel, while a client watching the
// session still follows it live.
func TestResolveTurnSinksSuppressesChunksForSystemWakes(t *testing.T) {
	chunked := chanSink("discord", "discord")
	chunked.Chunk = chunked.Send
	streamed := chanSink("web", "")
	streamed.Stream = func(StreamEvent) {}
	defaults := NewSinks(chunked, streamed)

	got := resolveTurnSinks(WakeCron, SinkSet{}, defaults)
	if got.HasChunk() {
		t.Error("a system wake must not deliver intermediate chunks to a channel")
	}
	if !got.HasStream() {
		t.Error("a system wake must still stream live to a watching client")
	}

	// A human speaking keeps chunked delivery.
	if !resolveTurnSinks(WakeDiscord, SinkSet{}, defaults).HasChunk() {
		t.Error("a user wake must keep chunked delivery")
	}
}

// TestContentSinkProactive pins what is left of contentSink once the turn's
// destinations are resolved up front: a silent-source gate and the bookkeeping
// flag that tells recordProactiveChat to write chat.jsonl itself.
//
// Proactive means content reaches a human with no inbound message of theirs to
// answer — cron, a peer session, a progress note — so no origin sink's Flush
// records it.
func TestContentSinkProactive(t *testing.T) {
	turn := NewSinks(chanSink("discord", "channel"))

	cases := []struct {
		name       string
		sessionKey string
		source     WakeSource
		silent     bool
		proactive  bool
	}{
		{"the human just spoke", "cli", WakeWeb, false, false},
		{"cron speaks to the human", "cli", WakeCron, false, true},
		{"peer session speaks to the human", "cli", WakeSession, false, true},
		{"progress speaks to the human", "cli", WakeProgress, false, true},
		// Deliberately redundant with the empty set resolveTurnSinks builds:
		// nightly maintenance speaking to the user is the most expensive leak
		// this system has, so it is gated twice.
		{"heartbeat is silent", "cli", WakeHeartbeat, true, false},
		{"legacy heartbeat source is silent", "cli", WakeSource("heartbeat_reflect"), true, false},
		{"compression is silent", "cli", WakeCompression, true, false},
		// Sessions with no human of their own never record proactive chat: a
		// subagent's output is addressed to its parent, not to a person.
		{"subagent is not proactive", "cli:threads:job", WakeSession, false, false},
		{"cron session is not proactive", "cron:tidyup", WakeCron, false, false},
		{"quote sibling is not proactive", "web:abc" + session.QuoteSessionSuffix, WakeQuote, false, false},
		{"pin sibling is not proactive", "web:abc" + session.PinSessionSuffix, WakePin, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := &Thread{sessionKey: tc.sessionKey, lastWakeSource: tc.source}
			got, proactive := th.contentSink(turn)
			if got.IsZero() != tc.silent {
				t.Fatalf("silent = %v, want %v", got.IsZero(), tc.silent)
			}
			if proactive != tc.proactive {
				t.Errorf("proactive = %v, want %v", proactive, tc.proactive)
			}
		})
	}
}

// TestResolveDeliveryLabel pins the wake payload's `delivery` field: the one
// place a turn is told where its output goes (how-nagobot-works.md), so it must
// name every destination that actually routes the content and nothing else.
func TestResolveDeliveryLabel(t *testing.T) {
	const discord = "your response will be sent to discord channel 123"
	const wecom = "your response will be sent to the user via wecom"

	cases := []struct {
		name   string
		source WakeSource
		sinks  SinkSet
		want   string
	}{
		{
			// A foreign-channel message names both real destinations. The web
			// mirror carries no label on purpose — it is not a routing choice,
			// and naming it would put a line of noise in every turn on every
			// channel for something the model must not reason about.
			name:   "every real destination is named, the mirror is not",
			source: WakeWeCom,
			sinks:  NewSinks(chanSink("wecom", wecom), chanSink("discord", discord), chanSink("web", "")),
			want:   wecom + "; " + discord,
		},
		{
			name:   "one destination reads exactly as before",
			source: WakeDiscord,
			sinks:  NewSinks(chanSink("discord", discord)),
			want:   discord,
		},
		{
			// Maintenance reaches nobody and must say so.
			name:   "an empty set names nobody",
			source: WakeHeartbeat,
			sinks:  SinkSet{},
			want:   noDeliveryLabel,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := &Thread{sessionKey: "discord:123", lastWakeSource: tc.source}
			if got := th.resolveDeliveryLabel(tc.sinks); got != tc.want {
				t.Fatalf("delivery = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRecordProactiveChatWritesChatLog verifies bot-initiated content is written
// to the clean chat log. It is delivered on the channel sink, which bypasses the
// per-wake chat.jsonl sink — without this, cron follow-ups and progress notes
// would be invisible to pre-think's recent-chat context.
func TestRecordProactiveChatWritesChatLog(t *testing.T) {
	sessMgr, err := session.NewManager(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}
	mgr := NewManager(&ThreadConfig{Sessions: sessMgr})

	th := &Thread{sessionKey: "cli", mgr: mgr, lastWakeSource: WakeCron}
	th.recordProactiveChat("proactive hello")

	got := session.ReadRecentChat(mgr.SessionDir("cli"), 5, time.Local)
	if !strings.Contains(got, "assistant: proactive hello") {
		t.Errorf("chat.jsonl missing assistant entry; got %q", got)
	}
	// The rendered form omits the origin, so assert it on the raw record: it is
	// what lets a reader tell a bot-initiated message from a plain reply.
	raw, err := os.ReadFile(filepath.Join(mgr.SessionDir("cli"), "chat.jsonl"))
	if err != nil {
		t.Fatalf("read chat.jsonl: %v", err)
	}
	if !strings.Contains(string(raw), string(WakeCron)) {
		t.Errorf("chat.jsonl should record the driving source %q; got %s", WakeCron, raw)
	}
}
