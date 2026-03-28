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
var resumableChannelPrefixes = []string{"telegram:", "discord:", "feishu:", "cron:"}

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

		// Quick exit: if last message is assistant without tool_calls, session completed normally.
		if lastMsg.Role == "assistant" && len(lastMsg.ToolCalls) == 0 {
			return nil
		}

		// Layer 3: full load for definitive check.
		// ReadFile returns already-sanitized messages, so no need to re-sanitize.
		sess, err := session.ReadFile(path)
		if err != nil {
			return nil
		}
		if !isIncompleteSession(sess.Messages) {
			return nil
		}

		// Find the original user message — extract both body and agent from
		// the same message to guarantee consistency (#108).
		origMsg, ok := findLastUserMessage(sess.Messages)
		body := ""
		agent := ""
		if ok {
			body = origMsg.Content
			if runes := []rune(body); len(runes) > 1000 {
				body = string(runes[:1000]) + "\n... (truncated)"
			}
			if yamlBlock, _, fmOk := thread.SplitFrontmatter(origMsg.Content); fmOk {
				agent = thread.ExtractFrontmatterValue(yamlBlock, "agent")
			}
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

// isIncompleteSession returns true if the sanitized message history indicates
// an interrupted turn. Expects already-sanitized messages (from session.ReadFile).
func isIncompleteSession(messages []provider.Message) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	// A completed turn ends with an assistant message that has no tool_calls.
	if last.Role == "assistant" && len(last.ToolCalls) == 0 {
		return false
	}
	// A turn ending with sleep_thread is a deliberate completion (e.g., heartbeat,
	// compression, or a resume that the LLM chose to skip). Not an interruption.
	if last.Role == "tool" && last.Name == "sleep_thread" {
		return false
	}
	return true
}

// findLastUserMessage scans backwards for the last role=user message,
// skipping resume-source messages to find the original request.
func findLastUserMessage(messages []provider.Message) (provider.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Source != "resume" {
			return messages[i], true
		}
	}
	return provider.Message{}, false
}

