package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

const (
	compressMinContentLen  = 200 // skip short tool results (idempotency)
	compressKeepAssistants = 3   // protect last N assistant turns
)

// runCompressionScan scans idle threads and applies the appropriate compression tier:
//   - Tier 1 (idle 5-30min, >50% tokens): mechanical tool-result compression
//   - Tier 2 (idle ≥30min, >60% tokens): AI-driven silent compression via compress-context skill
func (m *Manager) runCompressionScan() {
	cfg := m.cfg
	if cfg.Sessions == nil || cfg.ContextWindowTokens <= 0 {
		return
	}

	type candidate struct {
		key  string
		idle time.Duration
	}

	m.mu.Lock()
	var candidates []candidate
	now := time.Now()
	for key, t := range m.threads {
		idle := now.Sub(t.lastActiveAt)
		if t.state == threadIdle && idle >= tier1IdleMin && idle < tier2IdleMax {
			candidates = append(candidates, candidate{key: key, idle: idle})
		}
	}
	m.mu.Unlock()

	if len(candidates) == 0 {
		return
	}

	for _, c := range candidates {
		if c.idle >= tier2IdleMin {
			m.tryTier2Compress(c.key)
		} else {
			// Tier 1: mechanical compression (5-30min idle), no token threshold
			m.tryTier1Compress(c.key)
		}
	}
}

// tryTier1Compress performs mechanical tool-result compression on an idle session.
// No token threshold — always compresses tool results when idle 5-30min.
func (m *Manager) tryTier1Compress(sessionKey string) {
	cfg := m.cfg
	sess, err := cfg.Sessions.Reload(sessionKey)
	if err != nil {
		logger.Debug("tier1 compress: failed to load session", "sessionKey", sessionKey, "err", err)
		return
	}
	if len(sess.Messages) == 0 {
		return
	}

	modified, newMessages := compressToolResults(sess.Messages, compressKeepAssistants)
	if !modified {
		return
	}

	sessionPath := cfg.Sessions.PathForKey(sessionKey)
	if err := backupSession(sessionPath); err != nil {
		logger.Warn("tier1 compress: backup failed", "sessionKey", sessionKey, "err", err)
		return
	}

	sess.Messages = newMessages
	if err := cfg.Sessions.Save(sess); err != nil {
		logger.Warn("tier1 compress: save failed", "sessionKey", sessionKey, "err", err)
		return
	}

	logger.Info("tier1 compress: tool results trimmed",
		"sessionKey", sessionKey,
		"messageCount", len(newMessages),
	)
}

// tryTier2Compress silently wakes the thread to run AI-driven compression.
func (m *Manager) tryTier2Compress(sessionKey string) {
	cfg := m.cfg

	sess, err := cfg.Sessions.Reload(sessionKey)
	if err != nil || len(sess.Messages) == 0 {
		return
	}

	tokens := estimateMessagesTokens(sess.Messages)
	threshold := int(float64(cfg.ContextWindowTokens) * tier2TokenRatio)
	if tokens < threshold {
		return
	}

	m.mu.Lock()
	t, ok := m.threads[sessionKey]
	if !ok || t.state != threadIdle {
		m.mu.Unlock()
		return
	}
	// Skip if recently compressed (token-based guard failed, e.g. skill didn't execute).
	if !t.lastCompressedAt.IsZero() && time.Since(t.lastCompressedAt) < 10*time.Minute {
		m.mu.Unlock()
		return
	}
	t.lastCompressedAt = time.Now()
	m.mu.Unlock()

	sessionPath := cfg.Sessions.PathForKey(sessionKey)
	instruction := msg.BuildSystemMessage("compression_maintenance", map[string]string{
		"session_key":      sessionKey,
		"session_file":     sessionPath,
		"estimated_tokens": fmt.Sprintf("%d", tokens),
		"context_window":   fmt.Sprintf("%d", cfg.ContextWindowTokens),
		"usage_ratio":      fmt.Sprintf("%.2f", float64(tokens)/float64(cfg.ContextWindowTokens)),
	}, `This is an automated background maintenance task. You MUST load and execute skill "compress-context" NOW. Use the session_file path provided above. Do NOT produce any user-facing output. Just compress and finish.`)

	t.Enqueue(&WakeMessage{
		Source:  WakeCompression,
		Message: instruction,
		Sink: Sink{
			Label: "maintenance task, response will not be delivered to any user",
			Send:  func(_ context.Context, _ string) error { return nil },
		},
	})

	logger.Info("tier2 compress: AI compression wake enqueued",
		"sessionKey", sessionKey,
		"tokens", tokens,
		"threshold", threshold,
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
		if msg.Compressed != "" || len(msg.Content) <= compressMinContentLen {
			continue
		}
		msg.Compressed = fmt.Sprintf(`<compressed tool="%s" original="%d"/>`, msg.Name, len(msg.Content))
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
