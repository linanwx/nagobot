package msg

import (
	"sync"
	"time"

	"github.com/linanwx/nagobot/provider"
)

// StreamEventType identifies the kind of rich streaming event a turn emits
// toward a stream-capable sink (currently the web channel).
type StreamEventType string

const (
	StreamThinking   StreamEventType = "thinking"    // reasoning text delta
	StreamText       StreamEventType = "text"        // content text delta
	StreamToolCall   StreamEventType = "tool_call"   // tool execution started
	StreamToolResult StreamEventType = "tool_result" // tool execution finished
	StreamRoundEnd   StreamEventType = "round_end"   // one LLM call's stream closed
	StreamTurnEnd    StreamEventType = "turn_end"    // the whole turn finished

	// StreamMessageStart announces the id of the assistant message this round
	// is about to stream, before any of its content exists. Every delta of the
	// round carries the same id, and the entry eventually written to
	// session.jsonl carries it too — so a client addresses one message from
	// first token to persisted entry without ever re-keying it.
	StreamMessageStart StreamEventType = "message_start"

	// StreamMessage carries an entry that has just been written to
	// session.jsonl. It is how a client learns a message EXISTS: membership and
	// order of the conversation are the server's to declare, and guessing where
	// a message landed is what this event exists to end.
	StreamMessage StreamEventType = "message"
)

// StreamEvent is one unit of live turn activity. Text-bearing events carry
// both the increment and the round-accumulated snapshot: a client that missed
// or dropped deltas self-heals from the next snapshot (OpenClaw's design),
// so text events are safe to coalesce or drop under backpressure.
type StreamEvent struct {
	Type       StreamEventType
	Delta      string // thinking/text: this increment
	Snapshot   string // thinking/text: the round's accumulated text so far
	Tool       string // tool_call/tool_result: tool name
	ToolCallID string // tool_call/tool_result: pairing id
	Args       string // tool_call: arguments JSON; tool_result: result preview
	IsError    bool   // tool_result: the tool returned an error
	Seq        int    // per-pipe monotonic sequence, stamped by StreamPipe
	// MessageID names the message the event belongs to. On message_start /
	// message it is that message's id; on every in-round event (thinking, text,
	// tool_call, tool_result, round_end) it is the id of the assistant message
	// the round is building, so live content patches a message the client has
	// already been told about instead of creating one of its own.
	MessageID string
	// Message is the persisted entry, set on message events only.
	Message *provider.Message
}

// streamCoalesceDelay is how long the drain goroutine sleeps between batches,
// letting token-rate deltas pile up so one WS frame carries many tokens
// (~12 fps). Tool events ride the same cadence — imperceptible.
const streamCoalesceDelay = 80 * time.Millisecond

// StreamPipe decouples a producing turn from a consuming transport: Push
// never blocks (unbounded buffer, bounded in practice by one turn's output),
// a lazily-started drain goroutine forwards coalesced batches, and Flush
// lets an authoritative Send serialize behind every queued stream frame so
// the transport never sees a final response overtaken by stale deltas.
//
// The drain goroutine exits whenever the queue empties — an idle pipe holds
// no goroutine, so an abandoned sink leaks nothing.
type StreamPipe struct {
	mu      sync.Mutex
	cond    *sync.Cond
	queue   []StreamEvent
	running bool
	seq     int
	forward func(StreamEvent)
}

// NewStreamPipe creates a pipe that forwards events to fn (called from the
// pipe's own goroutine, one event at a time, in order). fn may block — a slow
// transport only ever stalls the pipe, never the producing turn.
func NewStreamPipe(fn func(StreamEvent)) *StreamPipe {
	p := &StreamPipe{forward: fn}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Push enqueues an event and returns immediately.
func (p *StreamPipe) Push(ev StreamEvent) {
	p.mu.Lock()
	p.seq++
	ev.Seq = p.seq
	p.queue = append(p.queue, ev)
	if !p.running {
		p.running = true
		go p.drain()
	}
	p.mu.Unlock()
}

// Flush blocks until every event pushed so far has been forwarded.
func (p *StreamPipe) Flush() {
	p.mu.Lock()
	for p.running || len(p.queue) > 0 {
		p.cond.Wait()
	}
	p.mu.Unlock()
}

func (p *StreamPipe) drain() {
	for {
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.running = false
			p.cond.Broadcast()
			p.mu.Unlock()
			return
		}
		batch := p.queue
		p.queue = nil
		p.mu.Unlock()

		for _, ev := range coalesceStreamEvents(batch) {
			p.forward(ev)
		}
		time.Sleep(streamCoalesceDelay)
	}
}

// coalesceStreamEvents merges consecutive text-bearing events of the same
// type into one (concatenated Delta, last Snapshot/Seq), so a fast token
// stream becomes a few frames per batch instead of one frame per token.
func coalesceStreamEvents(batch []StreamEvent) []StreamEvent {
	out := batch[:0]
	for _, ev := range batch {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.Type == ev.Type && (ev.Type == StreamThinking || ev.Type == StreamText) {
				last.Delta += ev.Delta
				last.Snapshot = ev.Snapshot
				last.Seq = ev.Seq
				continue
			}
		}
		out = append(out, ev)
	}
	return out
}
