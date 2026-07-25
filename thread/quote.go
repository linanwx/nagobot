package thread

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

const (
	// quoteAgent is the tools-disabled stateless sibling agent
	// (specialty: [fast]) that condenses a message into a one-line quote.
	// It routes through `fast` rather than `lowcost` because the two optimize
	// for different things: a human is watching a spinner, so latency beats
	// price here, and a thinking model spends its reasoning budget on a task
	// that needs none. Measured on the deployment, the same quote went from
	// 4354ms / 298 reasoning tokens to 1929ms / 0 on a "-instant" alias.
	quoteAgent = "quote-summary"
	// quoteTimeout bounds one quote sibling turn. A human is watching a spinner
	// while this runs, so the budget is much tighter than the background
	// summarizers'; past it the client is told the quote failed.
	quoteTimeout = 20 * time.Second
	// quoteInputCap bounds the source runes handed to the sibling. Well past
	// this the model has more than enough to characterize the message, and a
	// pasted-in wall of text would otherwise cost a full-size request.
	quoteInputCap = 4000
	// quoteOutputCap bounds the returned line. The agent is asked for one short
	// line; this is the backstop that keeps a runaway answer from becoming a
	// wall of blockquote in the composer.
	quoteOutputCap = 200
)

// ErrQuoteUnavailable reports that no quote generator is configured — the
// quote-summary agent template is missing (workspace not synced). Surfaced to
// the caller rather than falling back: there is no mechanical quote path, by
// design, and a silently degraded quote is worse than a visible failure.
var ErrQuoteUnavailable = errors.New("quote generator is not configured (missing " + quoteAgent + " agent)")

// Quote condenses text into a single line of markdown quote — the complete
// format including the leading "> " marker — by waking the stateless quote
// sibling of parentKey. It blocks for the result.
//
// The line is produced entirely by the model: nothing here inspects the source
// text's structure, so a table, a code block, long prose and a one-liner all go
// down the same path and come back as a short human-readable reference. The
// only post-processing is a whitespace collapse (the result must be ONE line)
// and a length backstop.
//
// Returns an error on timeout, on a missing agent, or when the model does not
// produce the required format — never a fallback quote.
func (mgr *Manager) Quote(ctx context.Context, parentKey, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("nothing to quote")
	}
	if mgr == nil {
		return "", ErrQuoteUnavailable
	}
	cfg := mgr.cfg
	if cfg == nil || cfg.Agents == nil || cfg.Agents.Def(quoteAgent) == nil {
		return "", ErrQuoteUnavailable
	}

	ch := make(chan string, 1)
	key := parentKey + session.QuoteSessionSuffix
	start := time.Now()
	mgr.Wake(key, &WakeMessage{
		Source:    WakeQuote,
		Message:   "Message to quote:\n\n" + truncateStr(text, quoteInputCap),
		AgentName: quoteAgent,
		Sink: Sink{
			Label: "quote session — result returns via callback, never delivered to a channel",
			Send:  func(context.Context, string) error { return nil },
		},
		OnComplete: func(response string) { ch <- response },
	})

	var raw string
	select {
	case raw = <-ch:
	case <-time.After(quoteTimeout):
		logger.Warn("quote timeout", "sessionKey", parentKey, "timeout", quoteTimeout)
		return "", errors.New("quote generation timed out")
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// One line, always: the composer preview and the sent message both assume a
	// single blockquote line. Collapsing runs of whitespace joins a stray second
	// line instead of dropping it.
	line := oneLine(raw)
	if !strings.HasPrefix(line, ">") {
		// The format is the agent's job (see the few-shot examples in its
		// template). Prefixing it here would silently paper over a broken
		// template or a mis-routed model, so it fails loudly instead.
		logger.Warn("quote missing leading marker", "sessionKey", parentKey, "output", truncateStr(line, 200))
		return "", errors.New("quote generator returned an unusable format")
	}
	line = truncateStr(line, quoteOutputCap)
	logger.Info("quote generated", "sessionKey", parentKey, "durationMs", time.Since(start).Milliseconds(), "len", len(line))
	return line, nil
}
