package cmd

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
)

const resumeMaxAge = 1 * time.Hour

// resumableChannelPrefixes are session key prefixes for channels with
// persistent delivery (defaultSink can reach the user after restart).
var resumableChannelPrefixes = []string{"telegram:", "discord:", "feishu:", "wecom:", "cron:"}

// resumeCandidate holds the data needed to wake an interrupted session.
type resumeCandidate struct {
	key   string
	body  string
	agent string
}

// scanInterruptedSessions identifies sessions that were interrupted mid-execution.
// This is a pure read operation — no wakes are sent.
func scanInterruptedSessions(sessionsDir string) []resumeCandidate {
	cutoff := time.Now().Add(-resumeMaxAge)
	var candidates []resumeCandidate

	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || d.Name() != session.SessionFileName {
			return nil
		}

		key := session.DeriveKeyFromPath(path)

		// Layer 1: prefix filter — resumable channels + their child threads.
		if !isResumableSessionKey(key) {
			return nil
		}

		// Layer 2: timestamp filter — read only the last message's timestamp.
		lastMsg, err := session.ReadLastMessage(path)
		if err != nil {
			return nil // empty or unreadable — skip
		}
		if lastMsg.Timestamp.Before(cutoff) {
			return nil
		}

		// Layer 3: full load → find the resumable user message, then check
		// if that specific turn completed. We must NOT check the absolute last
		// message, because a system turn (e.g. heartbeat) may have been
		// interrupted after the user's turn already completed — checking the
		// tail would incorrectly resume with an old user message.
		sess, err := session.ReadFile(path)
		if err != nil {
			return nil
		}

		origMsg, userIdx, ok := findLastUserMessage(sess.Messages)
		body := ""
		agent := ""
		if !ok || isUserTurnComplete(sess.Messages, userIdx) {
			return nil
		}

		body = origMsg.Content
		if runes := []rune(body); len(runes) > 1000 {
			body = string(runes[:1000]) + "\n... (truncated)"
		}
		if yamlBlock, _, fmOk := thread.SplitFrontmatter(origMsg.Content); fmOk {
			agent = thread.ExtractFrontmatterValue(yamlBlock, "agent")
		}

		logger.Info("found interrupted session",
			"sessionKey", key,
			"lastRole", lastMsg.Role,
			"lastTimestamp", lastMsg.Timestamp.Format(time.RFC3339),
			"agent", agent,
		)
		candidates = append(candidates, resumeCandidate{key: key, body: body, agent: agent})
		return nil
	})
	return candidates
}

// sendResumeWakes fires resume wakes for the given candidates.
func sendResumeWakes(mgr *thread.Manager, candidates []resumeCandidate) {
	for _, c := range candidates {
		logger.Info("resuming interrupted session", "sessionKey", c.key, "agent", c.agent)
		mgr.Wake(c.key, &thread.WakeMessage{
			Source:    thread.WakeResume,
			Message:   c.body,
			AgentName: c.agent,
		})
	}
	if len(candidates) > 0 {
		logger.Info("resume complete", "count", len(candidates))
	}
}

// isResumableSessionKey returns true if the session key belongs to a channel
// with persistent delivery. For child threads, checks the parent key.
func isResumableSessionKey(key string) bool {
	if strings.HasSuffix(key, session.RephraseSessionSuffix) {
		return false
	}
	checkKey := key
	if idx := strings.Index(key, ":threads:"); idx >= 0 {
		checkKey = key[:idx]
	}
	for _, prefix := range resumableChannelPrefixes {
		if strings.HasPrefix(checkKey, prefix) {
			return true
		}
	}
	return false
}

// isUserTurnComplete checks whether the turn that started at userMsgIdx
// completed. The turn's scope extends from the user message until the next
// non-injected user message (a new turn) or end of session.
func isUserTurnComplete(messages []provider.Message, userMsgIdx int) bool {
	// Find end of this turn: the next non-injected user message starts a new turn.
	turnEnd := len(messages)
	for j := userMsgIdx + 1; j < len(messages); j++ {
		if messages[j].Role == "user" && !isInjectedMessage(messages[j].Content) {
			turnEnd = j
			break
		}
	}
	// No response at all after the user message → incomplete.
	if turnEnd <= userMsgIdx+1 {
		return false
	}
	last := messages[turnEnd-1]
	if last.Role == "assistant" && len(last.ToolCalls) == 0 {
		return true
	}
	if last.Role == "tool" && last.Name == "sleep_thread" {
		return true
	}
	return false
}

// nonResumableSources are sources that should not be resumed after a crash.
// - heartbeat/compression: self-recovering, the system will re-trigger them
// Note: "resume" is intentionally NOT excluded. A completed resume's turn
// passes isUserTurnComplete, so it won't be re-triggered. Excluding it
// would cause the original interrupted message to be found instead,
// leading to infinite resume loops.
var nonResumableSources = map[string]bool{
	"heartbeat": true, "compression": true,
}

// isInjectedMessage checks the YAML frontmatter of a user message for
// the `injected: true` field, which marks messages that were injected
// mid-execution (between tool iterations) rather than initiating reasoning.
func isInjectedMessage(content string) bool {
	yamlBlock, _, ok := thread.SplitFrontmatter(content)
	if !ok {
		return false
	}
	return thread.ExtractFrontmatterValue(yamlBlock, "injected") == "true"
}

// findLastUserMessage scans backwards for the last role=user message that
// initiated a reasoning turn worth resuming. Returns the message and its index.
// Skips:
//   - Mid-execution injected messages (injected: true in frontmatter)
//   - Non-resumable sources (heartbeat, compression, resume)
func findLastUserMessage(messages []provider.Message) (provider.Message, int, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == "user" && !nonResumableSources[m.Source] && !isInjectedMessage(m.Content) {
			return m, i, true
		}
	}
	return provider.Message{}, -1, false
}

