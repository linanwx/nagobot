package thread

import (
	"testing"
	"time"
)

// TestTryMergeCollectsTraceparents pins the one place a merged-away message
// could lose its trace.
//
// tryMerge overwrites Sinks, CallerSink and MessageSink with the LAST message's
// values, because a merged turn is answered as a whole. Traceparent must NOT
// follow that pattern: the turn links to every merged message, so each one's
// trace has to survive the fold. Copying the sink behaviour here would leave
// every message but one pointing at a trace with no turn in it.
func TestTryMergeCollectsTraceparents(t *testing.T) {
	th := &Thread{
		inbox:  make(chan *WakeMessage, 8),
		signal: make(chan struct{}, 1),
	}

	first := &WakeMessage{Source: WakeTelegram, Message: "a", Traceparent: "tp-a"}
	th.inbox <- &WakeMessage{Source: WakeTelegram, Message: "b", Traceparent: "tp-b"}
	th.inbox <- &WakeMessage{Source: WakeTelegram, Message: "c", Traceparent: "tp-c"}

	got := th.tryMerge(first)

	if len(got.MergedTraceparents) != 2 {
		t.Fatalf("MergedTraceparents = %v, want the two folded-in wakes", got.MergedTraceparents)
	}
	if got.MergedTraceparents[0] != "tp-b" || got.MergedTraceparents[1] != "tp-c" {
		t.Fatalf("MergedTraceparents = %v, want [tp-b tp-c]", got.MergedTraceparents)
	}
	// The first message's own trace is the PARENT, not a link — it must stay
	// where it is rather than joining the link list.
	if got.Traceparent != "tp-a" {
		t.Fatalf("Traceparent = %q, want the first wake's own trace", got.Traceparent)
	}
}

// TestTryMergeSkipsEmptyTraceparents keeps untraced producers out of the link
// list. A cron seed or a hand-built WakeMessage carries no trace; recording an
// empty link would put a span with an all-zero trace id in the file.
func TestTryMergeSkipsEmptyTraceparents(t *testing.T) {
	th := &Thread{
		inbox:  make(chan *WakeMessage, 8),
		signal: make(chan struct{}, 1),
	}

	first := &WakeMessage{Source: WakeTelegram, Message: "a", Traceparent: "tp-a"}
	th.inbox <- &WakeMessage{Source: WakeTelegram, Message: "b"} // no trace
	th.inbox <- &WakeMessage{Source: WakeTelegram, Message: "c", Traceparent: "tp-c"}

	got := th.tryMerge(first)

	if len(got.MergedTraceparents) != 1 || got.MergedTraceparents[0] != "tp-c" {
		t.Fatalf("MergedTraceparents = %v, want only [tp-c]", got.MergedTraceparents)
	}
}

func TestQueueWaitMs(t *testing.T) {
	if got := queueWaitMs(time.Time{}); got != 0 {
		t.Fatalf("queueWaitMs(zero) = %d, want 0 — an unstamped wake has no measurable wait", got)
	}
	if got := queueWaitMs(time.Now().Add(-1500 * time.Millisecond)); got < 1400 || got > 1700 {
		t.Fatalf("queueWaitMs(1.5s ago) = %d, want ~1500", got)
	}
}
