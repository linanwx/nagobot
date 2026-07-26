package cmd

import (
	"context"
	"strings"
	"time"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

// webObserverSink returns the sink that mirrors a session's output to any
// browser watching it — the second member of that session's SinkSet, alongside
// the channel the conversation actually lives on.
//
// This is what makes the web console a window onto every session instead of a
// channel of its own. Before it, a page opened on a Discord group received
// exactly zero frames while that group's turn ran: the wake's sink pointed at
// Discord, so there was no Stream func, no rich stream, and StreamTo routed by
// the originating channel name. The page only caught up on its next history
// read.
//
// Two properties are load-bearing:
//
//   - Its Label is EMPTY, so it contributes nothing to the wake's `delivery`
//     statement. That field tells the model where its words go, and a mirror is
//     not a routing choice — naming it would add a line of noise to every turn
//     on every channel for something the model must not reason about.
//   - Silence is success. Almost no turn has a page open on it, so Observe (not
//     Send) is the delivery call: an unwatched mirror returns nil instead of
//     turning every Discord message into a partial-delivery warning.
func webObserverSink(chMgr *channel.Manager, sessionKey string) (thread.SessionSink, bool) {
	if chMgr == nil || !observableSession(sessionKey) {
		return thread.SessionSink{}, false
	}
	ch, ok := chMgr.Get("web")
	if !ok {
		return thread.SessionSink{}, false
	}
	obs, ok := ch.(channel.Observer)
	if !ok {
		return thread.SessionSink{}, false
	}

	// Same shape as the originating channel's own stream wiring: Push never
	// blocks the turn, a lazily-started drain goroutine forwards to whichever
	// pages are bound, and every authoritative message flushes the pipe first so
	// a final response is never overtaken by stale deltas.
	pipe := thread.NewStreamPipe(func(ev thread.StreamEvent) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := obs.ObserveStream(ctx, sessionKey, ev); err != nil {
			logger.Debug("web observer stream failed", "session", sessionKey, "err", err)
		}
	})

	return thread.SessionSink{
		Send: func(ctx context.Context, response string) error {
			if strings.TrimSpace(response) == "" {
				return nil
			}
			pipe.Flush()
			return obs.Observe(ctx, sessionKey, response)
		},
		Stream: pipe.Push,
	}, true
}

// observableSession reports whether a session may be mirrored to the web.
//
// Excluded, each for its own reason:
//   - web: sessions — the conversation already lives there; a second web sink
//     would deliver everything twice.
//   - internal sibling sessions (:quote, :pin, :imagepreview, …) — their output
//     is a value returned to a caller via OnComplete, not speech. Mirroring it
//     puts "created pins/foo.md" on a page, which is the same class of bug as
//     the contentSink one that routed sibling output to defaultSink.
//   - subagent / fork children — their output is addressed to the parent
//     session, which is itself mirrored. The user reads the parent's turn.
//
// Cron sessions are deliberately kept: their output is dropped by design, but a
// browser open on one is how you watch a scheduled job run.
func observableSession(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if strings.HasPrefix(key, "web:") {
		return false
	}
	if session.IsInternalSiblingSession(key) {
		return false
	}
	if strings.Contains(key, session.ThreadsSessionInfix) || strings.Contains(key, session.ForkSessionInfix) {
		return false
	}
	return true
}
