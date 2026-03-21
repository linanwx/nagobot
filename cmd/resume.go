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

const resumeMaxAge = 10 * time.Minute

// resumableChannelPrefixes are session key prefixes for channels with
// persistent delivery (defaultSink can reach the user after restart).
var resumableChannelPrefixes = []string{"telegram:", "discord:", "feishu:"}

// resumeInterruptedSessions scans sessions on disk and wakes any that were
// interrupted mid-execution within resumeMaxAge.
func resumeInterruptedSessions(sessionsDir string, mgr *thread.Manager) {
	cutoff := time.Now().Add(-resumeMaxAge)
	var resumed int

	err := filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || d.Name() != session.SessionFileName {
			return nil
		}

		key := session.DeriveKeyFromPath(path)

		// Layer 1: prefix filter — only telegram/discord/feishu, no child threads.
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

		// Find the original user message to reference in the resume payload.
		origMsg, ok := findLastUserMessage(sess.Messages)
		body := ""
		if ok {
			body = origMsg.Content
			// Truncate to avoid bloating the wake payload.
			if len(body) > 1000 {
				body = body[:1000] + "\n... (truncated)"
			}
		}

		logger.Info("resuming interrupted session",
			"sessionKey", key,
			"lastRole", lastMsg.Role,
			"lastTimestamp", lastMsg.Timestamp.Format(time.RFC3339),
		)

		mgr.Wake(key, &thread.WakeMessage{
			Source:  thread.WakeResume,
			Message: body,
		})
		resumed++
		return nil
	})
	if err != nil {
		logger.Error("resume scan failed", "err", err)
	}
	if resumed > 0 {
		logger.Info("resume scan complete", "resumed", resumed)
	}
}

// isResumableSessionKey returns true if the session key belongs to a channel
// with persistent delivery and is not a child thread.
func isResumableSessionKey(key string) bool {
	// Exclude child threads (e.g., "telegram:123:threads:abc").
	if strings.Contains(key, ":threads:") {
		return false
	}
	for _, prefix := range resumableChannelPrefixes {
		if strings.HasPrefix(key, prefix) {
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
	return true
}

// findLastUserMessage scans backwards for the last role=user message.
func findLastUserMessage(messages []provider.Message) (provider.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i], true
		}
	}
	return provider.Message{}, false
}
