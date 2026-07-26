package msg

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func recordingSink(label string, log *[]string, err error) SessionSink {
	return SessionSink{
		Label: label,
		Send: func(_ context.Context, text string) error {
			*log = append(*log, label+":send:"+text)
			return err
		},
	}
}

// TestSinkSetBroadcasts pins the reason a session's delivery is a set and not a
// single sink: every view of the session gets the output.
func TestSinkSetBroadcasts(t *testing.T) {
	var log []string
	set := NewSinks(
		recordingSink("a", &log, nil),
		recordingSink("b", &log, nil),
	)
	if err := set.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := strings.Join(log, ","); got != "a:send:hi,b:send:hi" {
		t.Fatalf("delivery = %q, want both sinks", got)
	}
	if set.Label() != "a; b" {
		t.Fatalf("Label = %q, want both labels", set.Label())
	}
}

// TestSinkSetPartialFailure pins the error contract agreed for broadcast: one
// dead destination must not silence the others, so only a total failure is an
// error. A partial failure is logged (not asserted here) and reported as
// success, because the message DID reach someone.
func TestSinkSetPartialFailure(t *testing.T) {
	boom := errors.New("boom")
	var log []string
	set := NewSinks(
		recordingSink("dead", &log, boom),
		recordingSink("live", &log, nil),
	)
	if err := set.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("partial failure must not surface as an error, got %v", err)
	}
	if len(log) != 2 {
		t.Fatalf("a failing sink must not short-circuit the rest, log = %v", log)
	}

	allDead := NewSinks(recordingSink("d1", &log, boom), recordingSink("d2", &log, boom))
	if err := allDead.Send(context.Background(), "hi"); err == nil {
		t.Fatal("delivery that reached nobody must return an error")
	}
}

// TestSinkSetUnion pins the assembly rule for a turn's destinations: the
// inbound channel's sink over the session's standing ones, deduplicated by
// Channel with the inbound side winning.
func TestSinkSetUnion(t *testing.T) {
	var log []string
	ch := func(name string) SessionSink {
		s := recordingSink(name, &log, nil)
		s.Channel = strings.SplitN(name, "/", 2)[0]
		return s
	}
	session := NewSinks(ch("discord/standing"), ch("web/mirror"))

	channels := func(s SinkSet) []string {
		out := make([]string, 0, s.Len())
		for i := range s.Len() {
			out = append(out, s.ChannelAt(i))
		}
		return out
	}

	// Foreign channel: three real destinations, nothing deduplicated.
	got := NewSinks(ch("wecom/inbound")).Union(session)
	if want := "wecom,discord,web"; strings.Join(channels(got), ",") != want {
		t.Fatalf("wecom union = %v, want %s", channels(got), want)
	}

	// Same channel: one destination, and it must be the INBOUND one — that is
	// the sink carrying this turn's chat.jsonl buffer.
	got = NewSinks(ch("discord/inbound")).Union(session)
	if want := "discord,web"; strings.Join(channels(got), ",") != want {
		t.Fatalf("discord union = %v, want %s", channels(got), want)
	}
	log = nil
	_ = got.Send(context.Background(), "x")
	if strings.Join(log, ",") != "discord/inbound:send:x,web/mirror:send:x" {
		t.Fatalf("the inbound sink must win the collision, got %v", log)
	}

	// A sink with no Channel is not a transport and is never deduplicated.
	paired := SessionSink{Label: "paired", Send: func(context.Context, string) error { return nil }}
	got = NewSinks(paired).Union(NewSinks(paired))
	if got.Len() != 2 {
		t.Fatalf("channel-less sinks must not deduplicate, got %d", got.Len())
	}
}

// TestSinkSetDropsSinkWithoutSend pins that a member with no authoritative
// delivery is not a destination — admitting it would make IsZero lie, and
// IsZero is what every "does anyone read this turn" check rests on.
func TestSinkSetDropsSinkWithoutSend(t *testing.T) {
	set := NewSinks(SessionSink{Label: "stream only", Stream: func(StreamEvent) {}})
	if !set.IsZero() {
		t.Fatalf("a sink with no Send must not count as a destination, len = %d", set.Len())
	}
}

// TestSinkSetLiveDeliveryModes pins the split that replaced the Chunkable bool:
// the runner asks the set which live modes exist, and SettleTurnContent asks for
// the destinations that got neither — the only ones it may still send to.
func TestSinkSetLiveDeliveryModes(t *testing.T) {
	var log []string
	chunked := recordingSink("chunked", &log, nil).Chunked()
	streamed := recordingSink("streamed", &log, nil)
	streamed.Stream = func(StreamEvent) {}
	terminal := recordingSink("terminal", &log, nil)

	set := NewSinks(chunked, streamed, terminal)
	if !set.HasChunk() || !set.HasStream() {
		t.Fatalf("HasChunk=%v HasStream=%v, want both", set.HasChunk(), set.HasStream())
	}

	// Chunk reaches only the chunk-registered sink, through the same function
	// Chunked() aliased to Send.
	log = nil
	if err := set.Chunk(context.Background(), "mid"); err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if got := strings.Join(log, ","); got != "chunked:send:mid" {
		t.Fatalf("Chunk delivery = %q, want the chunk sink only", got)
	}

	settle := set.WithoutLiveDelivery()
	if settle.Label() != "terminal" {
		t.Fatalf("WithoutLiveDelivery = %q, want the sink with neither mode", settle.Label())
	}

	// WithoutChunking suppresses intermediates without touching live streaming:
	// a system-initiated turn still renders live for a watching client.
	quiet := set.WithoutChunking()
	if quiet.HasChunk() {
		t.Fatal("WithoutChunking must clear chunk registration")
	}
	if !quiet.HasStream() {
		t.Fatal("WithoutChunking must leave rich streaming intact")
	}
	if quiet.WithoutLiveDelivery().Len() != 2 {
		t.Fatalf("after WithoutChunking the ex-chunk sink becomes terminal, got %d", quiet.WithoutLiveDelivery().Len())
	}
}
