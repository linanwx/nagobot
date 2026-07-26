package msg

import (
	"context"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// SessionSink is one durable view of a session: somewhere this session's output
// is delivered. A session may have several — the channel the conversation lives
// on, plus any other surface watching the same session — and output is
// broadcast to every one of them.
//
// A sink registers AT MOST ONE live-delivery mode, and the runner calls both
// unconditionally: which one actually fires is each sink's own decision, not the
// runner's.
//
//   - Chunk: intermediate whole messages as the turn progresses — telegram,
//     discord, feishu, wecom, socket.
//   - Stream: rich live frames (thinking/text deltas, tool events, persisted
//     entries) — web.
//
// A sink with neither is a terminal destination that only ever sees the
// authoritative Send: paired session sinks, cron/heartbeat drop sinks, the
// callback sinks of the internal sibling sessions. Those are precisely the
// destinations SettleTurnContent exists for.
type SessionSink struct {
	Label  string
	Send   func(ctx context.Context, response string) error // Authoritative delivery of a complete message. Every sink implements this one.
	Chunk  func(ctx context.Context, text string) error     // Optional; see above.
	Stream func(ev StreamEvent)                             // Optional; see above. Must never block — back it with a StreamPipe.
	Flush  func(ctx context.Context) error                  // Optional: end-of-turn signal; recorders commit buffered output here.
}

// Chunked returns a copy that accepts intermediate chunked delivery through the
// same function as Send. Every chunkable destination in this codebase does
// exactly that — the registration exists so the runner can ask "does this
// destination want intermediates?" without the sink carrying a bool that says so.
func (s SessionSink) Chunked() SessionSink {
	s.Chunk = s.Send
	return s
}

// SinkSet is a session's delivery fan-out: zero or more SessionSinks that all
// receive this session's output.
type SinkSet struct {
	sinks []SessionSink
}

// NewSinks builds a set. A member with no Send is dropped: a sink that cannot
// deliver the authoritative message is not a destination, and admitting it would
// make IsZero lie.
func NewSinks(list ...SessionSink) SinkSet {
	var out SinkSet
	for _, s := range list {
		if s.Send == nil {
			continue
		}
		out.sinks = append(out.sinks, s)
	}
	return out
}

// With returns a copy of the set with more destinations appended, applying the
// same admission rule as NewSinks.
func (s SinkSet) With(extra ...SessionSink) SinkSet {
	out := SinkSet{sinks: append([]SessionSink(nil), s.sinks...)}
	for _, k := range extra {
		if k.Send == nil {
			continue
		}
		out.sinks = append(out.sinks, k)
	}
	return out
}

// IsZero reports whether the set has no destination at all.
func (s SinkSet) IsZero() bool { return len(s.sinks) == 0 }

// Len returns the number of destinations.
func (s SinkSet) Len() int { return len(s.sinks) }

// Label renders the set's destinations as one human-readable string — the value
// the wake payload's `delivery` field shows the model.
func (s SinkSet) Label() string {
	parts := make([]string, 0, len(s.sinks))
	for _, k := range s.sinks {
		if k.Label != "" {
			parts = append(parts, k.Label)
		}
	}
	return strings.Join(parts, "; ")
}

// deliver runs one delivery mode against every sink that registered it,
// tolerating partial failure: one dead destination must not silence the others.
//
// An error comes back only when NOTHING was delivered — that is the one case the
// caller has to know about, and the caller logs it. A partial failure returns
// nil, so the sinks that missed out are named here or they vanish silently.
func (s SinkSet) deliver(op string, pick func(SessionSink) func() error) error {
	tried := 0
	var failures []error
	var failedLabels []string
	for _, k := range s.sinks {
		fn := pick(k)
		if fn == nil {
			continue
		}
		tried++
		if err := fn(); err != nil {
			failures = append(failures, err)
			failedLabels = append(failedLabels, k.Label)
		}
	}
	if tried == 0 || len(failures) == 0 {
		return nil
	}
	if len(failures) == tried {
		return failures[0]
	}
	logger.Warn("sink delivery partially failed",
		"op", op, "failed", strings.Join(failedLabels, "; "),
		"of", tried, "err", failures[0])
	return nil
}

// Send delivers the authoritative message to every destination.
func (s SinkSet) Send(ctx context.Context, response string) error {
	return s.deliver("send", func(k SessionSink) func() error {
		if k.Send == nil {
			return nil
		}
		return func() error { return k.Send(ctx, response) }
	})
}

// Chunk delivers an intermediate message to every destination that accepts one.
// No-op when none does.
func (s SinkSet) Chunk(ctx context.Context, text string) error {
	return s.deliver("chunk", func(k SessionSink) func() error {
		if k.Chunk == nil {
			return nil
		}
		return func() error { return k.Chunk(ctx, text) }
	})
}

// Flush signals end-of-turn to every destination that buffers output.
func (s SinkSet) Flush(ctx context.Context) error {
	return s.deliver("flush", func(k SessionSink) func() error {
		if k.Flush == nil {
			return nil
		}
		return func() error { return k.Flush(ctx) }
	})
}

// Stream pushes a live frame to every destination that watches live. Must never
// block: each Stream func is expected to be backed by a StreamPipe.
func (s SinkSet) Stream(ev StreamEvent) {
	for _, k := range s.sinks {
		if k.Stream != nil {
			k.Stream(ev)
		}
	}
}

// HasChunk reports whether any destination accepts intermediate messages.
func (s SinkSet) HasChunk() bool {
	for _, k := range s.sinks {
		if k.Chunk != nil {
			return true
		}
	}
	return false
}

// HasStream reports whether any destination watches live.
func (s SinkSet) HasStream() bool {
	for _, k := range s.sinks {
		if k.Stream != nil {
			return true
		}
	}
	return false
}

// WithoutChunking returns a copy with intermediate delivery disabled everywhere,
// keeping authoritative Send (and live streaming) intact. Used for
// system-initiated wakes, where only the final message should reach a channel.
func (s SinkSet) WithoutChunking() SinkSet {
	out := SinkSet{sinks: make([]SessionSink, len(s.sinks))}
	for i, k := range s.sinks {
		k.Chunk = nil
		out.sinks[i] = k
	}
	return out
}

// WithoutChunkSinks returns the subset of destinations that do not take chunked
// delivery — the ones the markdown streamer did not already push text to.
func (s SinkSet) WithoutChunkSinks() SinkSet {
	var out SinkSet
	for _, k := range s.sinks {
		if k.Chunk == nil {
			out.sinks = append(out.sinks, k)
		}
	}
	return out
}

// WithoutLiveDelivery returns the subset of destinations that have neither
// live-delivery mode — the ones the runner never pushed this turn's text to, and
// therefore the only ones SettleTurnContent may still send to.
func (s SinkSet) WithoutLiveDelivery() SinkSet {
	var out SinkSet
	for _, k := range s.sinks {
		if k.Chunk == nil && k.Stream == nil {
			out.sinks = append(out.sinks, k)
		}
	}
	return out
}

// WithRetry returns a copy whose Send is wrapped in exponential-backoff retry.
func (s SinkSet) WithRetry(maxAttempts int) SinkSet {
	out := SinkSet{sinks: make([]SessionSink, len(s.sinks))}
	for i, k := range s.sinks {
		original := k.Send
		k.Send = func(ctx context.Context, response string) error {
			var err error
			for attempt := range maxAttempts {
				if err = original(ctx, response); err == nil {
					return nil
				}
				if attempt < maxAttempts-1 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Duration(1<<attempt) * time.Second):
					}
				}
			}
			return err
		}
		out.sinks[i] = k
	}
	return out
}

// MessageSink carries the parts of delivery that belong to ONE inbound message
// rather than to the session. They address that specific message, so they can
// neither be broadcast to a session's other views nor rebuilt from a session key
// — which is exactly why they are separated from SessionSink.
type MessageSink struct {
	React ReactFunc // Fire-and-forget emoji reaction on the source message.
}

// IsZero reports whether nothing message-specific is registered.
func (m MessageSink) IsZero() bool { return m.React.IsZero() }
