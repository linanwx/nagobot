package cmd

import (
	"context"
	"testing"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/thread"
)

// A message posted through /api/send arrives on the web channel but must not
// be answered there: the web sink's Send would push a notification of its own
// (or broadcast one to every device when the session has no web participant),
// next to the Discord message the session already sends. The wake's set may
// hold only a recorder: no channel to win Union's dedup, no label to claim a
// destination, and a Send that never reaches a transport.
func TestStandingDeliveryGetsNoWebSink(t *testing.T) {
	web := channel.NewWebChannel(config.DefaultConfig(), nil)
	d := &Dispatcher{
		channels: channel.NewManager(), // web not registered: any SendTo would fail
		threads:  thread.NewManager(&thread.ThreadConfig{}),
	}
	msg := &channel.Message{
		ChannelID: "web:discord:123",
		Text:      "hi",
		Metadata:  map[string]string{"chat_id": "discord:123", channel.MetaStandingDelivery: "1"},
	}

	sinks, caller, msgSink := d.buildSinks(web, msg, "discord:123")
	if sinks.Len() != 1 || sinks.ChannelAt(0) != "" {
		t.Fatalf("want one channel-less recorder, got len=%d channel=%q", sinks.Len(), sinks.ChannelAt(0))
	}
	if sinks.Label() != "" {
		t.Fatalf("recorder must not name a destination, label = %q", sinks.Label())
	}
	if sinks.HasStream() || sinks.HasChunk() {
		t.Fatal("recorder must carry no live mode")
	}
	if err := sinks.Send(context.Background(), "reply"); err != nil {
		t.Fatalf("recorder Send reached a transport: %v", err)
	}
	if caller.Send != nil || !msgSink.IsZero() {
		t.Fatal("no caller sink or reaction for a send-api message")
	}

	// Without the flag the same message keeps its web delivery sink.
	delete(msg.Metadata, channel.MetaStandingDelivery)
	sinks, _, _ = d.buildSinks(web, msg, "discord:123")
	if sinks.ChannelAt(0) != "web" {
		t.Fatalf("ordinary web message lost its web sink: channel = %q", sinks.ChannelAt(0))
	}
}
