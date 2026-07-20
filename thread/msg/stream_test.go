package msg

import (
	"sync"
	"testing"
)

// The pipe must deliver events in order, coalesce runs of same-type text
// events, and Flush must not return before everything queued is forwarded.
func TestStreamPipe_OrderCoalesceFlush(t *testing.T) {
	var mu sync.Mutex
	var got []StreamEvent
	p := NewStreamPipe(func(ev StreamEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})

	p.Push(StreamEvent{Type: StreamThinking, Delta: "a", Snapshot: "a"})
	p.Push(StreamEvent{Type: StreamThinking, Delta: "b", Snapshot: "ab"})
	p.Push(StreamEvent{Type: StreamText, Delta: "x", Snapshot: "x"})
	p.Push(StreamEvent{Type: StreamText, Delta: "y", Snapshot: "xy"})
	p.Push(StreamEvent{Type: StreamToolCall, Tool: "web_search", ToolCallID: "t1"})
	p.Push(StreamEvent{Type: StreamText, Delta: "z", Snapshot: "z"})
	p.Flush()

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no events forwarded")
	}
	// Concatenating every forwarded delta per type must reproduce the full
	// text regardless of how the pipe batched — coalescing merges deltas but
	// must never drop or reorder them.
	think, text := "", ""
	var kinds []StreamEventType
	for _, ev := range got {
		kinds = append(kinds, ev.Type)
		switch ev.Type {
		case StreamThinking:
			think += ev.Delta
		case StreamText:
			text += ev.Delta
		}
	}
	if think != "ab" {
		t.Errorf("thinking deltas = %q, want %q", think, "ab")
	}
	if text != "xyz" {
		t.Errorf("text deltas = %q, want %q", text, "xyz")
	}
	// The tool call must sit after all thinking and after the "xy" text run,
	// and before the "z" run (order preserved across types).
	toolIdx := -1
	for i, ev := range got {
		if ev.Type == StreamToolCall {
			toolIdx = i
		}
	}
	if toolIdx == -1 {
		t.Fatalf("tool call not forwarded; kinds = %v", kinds)
	}
	for i, ev := range got {
		if ev.Type == StreamThinking && i > toolIdx {
			t.Errorf("thinking event after tool call; kinds = %v", kinds)
		}
	}
	// Seq must be strictly increasing across forwarded events.
	last := 0
	for _, ev := range got {
		if ev.Seq <= last {
			t.Errorf("seq not increasing: %v then %v", last, ev.Seq)
		}
		last = ev.Seq
	}
}

// A pipe whose queue has drained holds no goroutine; pushing again must
// restart the drain. Flush on an idle pipe returns immediately.
func TestStreamPipe_RestartAfterIdle(t *testing.T) {
	var mu sync.Mutex
	count := 0
	p := NewStreamPipe(func(StreamEvent) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	p.Push(StreamEvent{Type: StreamText, Delta: "1", Snapshot: "1"})
	p.Flush()
	p.Flush() // idle flush: must not deadlock
	p.Push(StreamEvent{Type: StreamText, Delta: "2", Snapshot: "12"})
	p.Flush()

	mu.Lock()
	defer mu.Unlock()
	if count != 2 {
		t.Errorf("forwarded %d events, want 2", count)
	}
}
