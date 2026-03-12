package thread

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
	"gopkg.in/yaml.v3"
)

const (
	compressMinContentLen  = 2000 // skip short tool results (idempotency)
	compressKeepAssistants = 3    // protect last N assistant turns
	softTrimHeadChars      = 1500 // chars kept from start of result
	softTrimTailChars      = 1500 // chars kept from end of result
)

// runCompressionScan scans idle threads and applies the appropriate compression tier:
//   - Tier 1 (idle 5-30min, >50% tokens): mechanical tool-result compression
//   - Tier 2 (idle ≥30min, >60% tokens): AI-driven silent compression via context-ops skill
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

	toolModified, newMessages := compressToolResults(sess.Messages, compressKeepAssistants)
	wakeModified, newMessages := trimWakeFields(newMessages, compressKeepAssistants)
	if !toolModified && !wakeModified {
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

	tokens := EstimateMessagesTokens(ApplyCompressed(sess.Messages))
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
	}, `This is an automated background maintenance task. You MUST load and execute skill "context-ops" NOW. Use the session_file path provided above. Do NOT produce any user-facing output. Reply with COMPRESS_OK when done.`)

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

// trimWakeFields strips redundant fields from older wake messages.
// Keeps the last keepLast wake messages intact; older ones lose
// thread, session, delivery, and action fields from YAML frontmatter.
func trimWakeFields(messages []provider.Message, keepLast int) (bool, []provider.Message) {
	// Walk backward, count wake user messages (detected by YAML frontmatter).
	wakeCount := 0
	trimTargets := map[int]bool{}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.HasPrefix(messages[i].Content, "---\n") {
			wakeCount++
			if wakeCount > keepLast {
				trimTargets[i] = true
			}
		}
	}
	if len(trimTargets) == 0 {
		return false, messages
	}

	modified := false
	result := make([]provider.Message, len(messages))
	copy(result, messages)
	for i := range result {
		if !trimTargets[i] {
			continue
		}
		content := trimFrontmatterFields(result[i].Content, wakeTrimFields)
		if content != result[i].Content {
			result[i].Content = content
			modified = true
		}
	}
	return modified, result
}

// wakeTrimFields are the frontmatter keys to strip during compression.
var wakeTrimFields = map[string]bool{
	"thread": true, "session": true, "delivery": true, "action": true,
}

// trimFrontmatterFields removes specified keys from a YAML frontmatter block.
func trimFrontmatterFields(content string, removeKeys map[string]bool) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx < 0 {
		return content
	}
	endIdx += 4 // offset from the initial "---\n"

	yamlBlock := content[4:endIdx]
	body := content[endIdx+5:] // skip "\n---\n"

	var kept []string
	for _, line := range strings.Split(yamlBlock, "\n") {
		key := strings.SplitN(line, ":", 2)[0]
		if removeKeys[strings.TrimSpace(key)] {
			continue
		}
		kept = append(kept, line)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(strings.Join(kept, "\n"))
	sb.WriteString("\n---\n")
	sb.WriteString(body)
	return sb.String()
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

	// Pre-scan: find the last occurrence of each skill load.
	lastSkillLoad := make(map[string]int) // skill name → last message index
	for i, m := range messages {
		if m.Role == "tool" && m.Name == "use_skill" {
			if name := extractSkillName(m.Content); name != "" {
				lastSkillLoad[name] = i
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
		if msg.Compressed != "" {
			continue
		}
		// use_skill: compress if a newer load of the same skill exists.
		if msg.Name == "use_skill" {
			if skillName := extractSkillName(msg.Content); skillName != "" && lastSkillLoad[skillName] > i {
				msg.Compressed = marshalCompressed(compressedHeader{
					Compressed: "use_skill", Skill: skillName,
					Original: len(msg.Content), Outdated: true,
				}, "")
				modified = true
			}
			continue
		}
		if len(msg.Content) <= compressMinContentLen {
			continue
		}
		if strings.Contains(msg.Content, "<<media:") {
			continue
		}
		n := len(msg.Content)
		if n > softTrimHeadChars+softTrimTailChars {
			head := msg.Content[:softTrimHeadChars]
			tail := msg.Content[n-softTrimTailChars:]
			trimmed := n - softTrimHeadChars - softTrimTailChars
			msg.Compressed = marshalCompressed(compressedHeader{
				Compressed: msg.Name, Original: n, Trimmed: trimmed,
			}, head+"\n\n[trimmed]\n\n"+tail)
		} else {
			msg.Compressed = marshalCompressed(compressedHeader{
				Compressed: msg.Name, Original: n,
			}, "")
		}
		modified = true
	}

	return modified, result
}

// compressedHeader is the YAML frontmatter for compressed tool results.
type compressedHeader struct {
	Compressed string `yaml:"compressed"`
	Skill      string `yaml:"skill,omitempty"`
	Original   int    `yaml:"original"`
	Trimmed    int    `yaml:"trimmed,omitempty"`
	Outdated   bool   `yaml:"outdated,omitempty"`
}

// marshalCompressed builds a YAML-frontmatter compressed marker with optional body.
func marshalCompressed(h compressedHeader, body string) string {
	yamlBytes, _ := yaml.Marshal(h)
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(yamlBytes)
	sb.WriteString("---")
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}
	return sb.String()
}

// extractSkillName parses the skill name from a use_skill result's YAML frontmatter.
func extractSkillName(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx < 0 {
		return ""
	}
	for _, line := range strings.Split(content[4:4+endIdx], "\n") {
		if strings.HasPrefix(line, "skill: ") {
			return strings.TrimSpace(line[7:])
		}
	}
	return ""
}

// backupSession writes the current session file to the history directory.
func backupSession(sessionPath string) error {
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("session file is empty")
	}

	historyDir := filepath.Join(filepath.Dir(sessionPath), "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return err
	}

	now := time.Now()
	filename := fmt.Sprintf("%d_%s.jsonl", now.Unix(), now.Format("20060102T150405-0700"))
	return os.WriteFile(filepath.Join(historyDir, filename), data, 0644)
}
