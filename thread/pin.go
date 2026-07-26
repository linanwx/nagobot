package thread

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

const (
	// pinAgent is the sibling agent that files a pinned message into the parent
	// session's pins/ directory. Unlike the other siblings it keeps its tools —
	// reading the existing pins, deciding whether the new material belongs in
	// one of them, and writing the file IS the whole job. It routes through
	// `toolcall` for the same reason: this is a tool-driven turn, not a prose one.
	pinAgent = "pin-writer"
	// pinsDirName is the per-session directory holding the pinned notes.
	pinsDirName = "pins"
	// pinInputCap bounds the source runes handed to the sibling. Past this the
	// agent has more than enough to write a note, and a pinned wall of text
	// would otherwise cost a full-size request on every pin.
	pinInputCap = 8000
)

// ErrPinUnavailable reports that no pin agent is configured — the pin-writer
// agent template is missing (workspace not synced). Surfaced to the caller
// rather than silently dropping the pin: a button that reports success while
// nothing is ever written is worse than one that reports it cannot.
var ErrPinUnavailable = errors.New("pin filing is not configured (missing " + pinAgent + " agent)")

// pinPrompt is the fixed instruction handed to the pin agent. Everything about
// HOW to file — dedup against existing pins, the frontmatter shape, merging —
// lives in the agent template; this only says where the directory is and what
// the material is.
const pinPrompt = `Pins directory: %s

File the following message into that directory.

--- BEGIN PINNED MESSAGE ---
%s
--- END PINNED MESSAGE ---`

// Pin files a message into {sessionDir}/pins by waking the pin sibling of
// parentKey. It returns as soon as the wake is enqueued — the actual filing is
// an agentic turn with tool calls and takes a while, and the caller (a button
// in a browser) is acknowledging a request, not awaiting a result.
//
// Concurrency is handled by the sibling's own inbox: every pin for a session
// lands in the same thread, so two pins never race to merge into the same file.
// That serialization is the reason this runs in a sibling session at all rather
// than as an agent override on the user's own thread.
//
// The outcome is logged on completion (never delivered anywhere) so a failing
// agent is visible in the daemon log rather than silently swallowed.
func (mgr *Manager) Pin(parentKey, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("nothing to pin")
	}
	if mgr == nil {
		return ErrPinUnavailable
	}
	cfg := mgr.cfg
	if cfg == nil || cfg.Agents == nil || cfg.Agents.Def(pinAgent) == nil {
		return ErrPinUnavailable
	}

	dir := mgr.SessionDir(parentKey)
	if dir == "" {
		return errors.New("session directory is unavailable")
	}
	pinsDir := filepath.Join(dir, pinsDirName)
	// Created here rather than left to the agent: the listing endpoint and the
	// agent's own "read what is already there" step both want the directory to
	// exist, and a mkdir is not a decision worth spending a tool call on.
	if err := os.MkdirAll(pinsDir, 0755); err != nil {
		return err
	}

	start := time.Now()
	mgr.Wake(parentKey+session.PinSessionSuffix, &WakeMessage{
		Source:    WakePin,
		Message:   fmt.Sprintf(pinPrompt, pinsDir, truncateStr(text, pinInputCap)),
		AgentName: pinAgent,
		Sinks: NewSinks(SessionSink{
			Label: "pin session — the pin is written to disk by the agent, never delivered to a channel",
			Send:  func(context.Context, string) error { return nil },
		}),
		OnComplete: func(response string) {
			logger.Info("pin filed",
				"sessionKey", parentKey,
				"durationMs", time.Since(start).Milliseconds(),
				"result", truncateStr(oneLine(response), 300),
			)
		},
	})
	logger.Info("pin queued", "sessionKey", parentKey, "dir", pinsDir, "len", len(text))
	return nil
}
