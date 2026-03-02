package thread

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

const (
	compressIdleMinAge     = 30 * time.Minute // idle longer than this
	compressIdleMaxAge     = 24 * time.Hour   // but active within this
	compressTokenRatio     = 0.5              // token usage threshold
	compressMinContentLen  = 200              // skip short tool results (idempotency)
	compressKeepAssistants = 3                // protect last N assistant turns
)

// compressIdleSessions scans threads for idle sessions eligible for
// background tool-result compression.
func (m *Manager) compressIdleSessions() {
	cfg := m.cfg
	if cfg.Sessions == nil || cfg.ContextWindowTokens <= 0 {
		return
	}

	// Collect eligible session keys under lock.
	m.mu.Lock()
	var candidates []string
	now := time.Now()
	for key, t := range m.threads {
		idle := now.Sub(t.lastActiveAt)
		if t.state == threadIdle && idle > compressIdleMinAge && idle < compressIdleMaxAge {
			candidates = append(candidates, key)
		}
	}
	m.mu.Unlock()

	if len(candidates) == 0 {
		return
	}

	threshold := int(float64(cfg.ContextWindowTokens) * compressTokenRatio)

	for _, key := range candidates {
		m.tryCompressSession(key, threshold)
	}
}

func (m *Manager) tryCompressSession(sessionKey string, tokenThreshold int) {
	cfg := m.cfg
	sess, err := cfg.Sessions.Reload(sessionKey)
	if err != nil {
		logger.Debug("tier2 compress: failed to load session", "sessionKey", sessionKey, "err", err)
		return
	}
	if len(sess.Messages) == 0 {
		return
	}

	tokens := estimateMessagesTokens(sess.Messages)
	if tokens < tokenThreshold {
		return
	}

	modified, newMessages := compressToolResults(sess.Messages, compressKeepAssistants)
	if !modified {
		return
	}

	// Backup before modifying.
	sessionPath := cfg.Sessions.PathForKey(sessionKey)
	if err := backupSession(sessionPath); err != nil {
		logger.Warn("tier2 compress: backup failed", "sessionKey", sessionKey, "err", err)
		return
	}

	sess.Messages = newMessages
	if err := cfg.Sessions.Save(sess); err != nil {
		logger.Warn("tier2 compress: save failed", "sessionKey", sessionKey, "err", err)
		return
	}

	newTokens := estimateMessagesTokens(newMessages)
	logger.Info("tier2 compress: tool results trimmed",
		"sessionKey", sessionKey,
		"tokensBefore", tokens,
		"tokensAfter", newTokens,
		"messageCount", len(newMessages),
	)
}

// compressToolResults mechanically trims tool result messages.
// Messages within the last keepLastAssistants assistant turns are protected.
// Tool results with content <= compressMinContentLen are skipped (idempotent).
func compressToolResults(messages []provider.Message, keepLastAssistants int) (bool, []provider.Message) {
	// Find protection boundary: walk backward, count assistant messages.
	protectFrom := len(messages)
	assistantsSeen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			assistantsSeen++
			if assistantsSeen >= keepLastAssistants {
				protectFrom = i
				break
			}
		}
	}

	modified := false
	result := make([]provider.Message, len(messages))
	copy(result, messages)

	for i := 0; i < protectFrom; i++ {
		msg := &result[i]
		if msg.Role != "tool" {
			continue
		}
		if len(msg.Content) <= compressMinContentLen {
			continue
		}
		msg.Content = fmt.Sprintf("[tool: %s, %d chars]", msg.Name, len(msg.Content))
		modified = true
	}

	return modified, result
}

// backupSession writes the current session file to the history directory.
func backupSession(sessionPath string) error {
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return err
	}
	// Verify it's valid JSON before backing up.
	if !json.Valid(data) {
		return fmt.Errorf("session file is not valid JSON")
	}

	historyDir := filepath.Join(filepath.Dir(sessionPath), "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return err
	}

	now := time.Now()
	filename := fmt.Sprintf("%d_%s.json", now.Unix(), now.Format("20060102T150405-0700"))
	return os.WriteFile(filepath.Join(historyDir, filename), data, 0644)
}
