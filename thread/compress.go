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
	compressMinContentLen  = 2000 // skip short content
	compressKeepAssistants = 3    // protect last N assistant turns
	softTrimHeadChars      = 1500 // chars kept from start of result
	softTrimTailChars      = 1500 // chars kept from end of result

	compressedHintFmt  = "[compressed — use search-memory --context %s --full to see content if needed, use skill session-ops to see more]"
	compressedHintNoID = "[compressed — use search-memory with session key and timeframe to find original content, or use skill session-ops to see more]"
)

// runCompressionScan scans idle threads and applies the appropriate compression tier:
//   - Tier 1 (idle 5-30min): mechanical compression of tools and large assistant-only user messages
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
			m.tryTier1Compress(c.key)
		}
	}
}

// tryTier1Compress performs mechanical compression on an idle session.
// No token threshold — always runs when idle 5-30min.
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

	modified, newMessages := compressTier1(sess.Messages, compressKeepAssistants)
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

	logger.Info("tier1 compress: compression applied",
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

// compressTier1 performs unified mechanical compression on all message types.
// Results are always written to Compressed; Content is never modified.
// Always recomputes from original Content (idempotent — same Content → same Compressed).
func compressTier1(messages []provider.Message, keepLastAssistants int) (bool, []provider.Message) {
	// Find protection boundary: walk backward, count assistant turns.
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

	// Pre-scan: find the last occurrence of each skill load (for outdated detection).
	lastSkillLoad := make(map[string]int)
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
		m := &result[i]
		var newCompressed string

		switch m.Role {
		case "tool":
			newCompressed = computeToolCompressed(m, i, lastSkillLoad)
		case "user":
			if strings.HasPrefix(m.Content, "---\n") {
				newCompressed = computeWakeCompressed(m)
			}
		}

		if newCompressed != m.Compressed {
			m.Compressed = newCompressed
			modified = true
		}
	}

	return modified, result
}

// computeToolCompressed returns the Compressed value for a tool message.
// Returns "" if no compression is needed.
func computeToolCompressed(m *provider.Message, idx int, lastSkillLoad map[string]int) string {
	if m.Name == "use_skill" {
		skillName := extractSkillName(m.Content)
		if skillName != "" && lastSkillLoad[skillName] > idx {
			return marshalCompressed(compressedHeader{
				Compressed: "use_skill", Skill: skillName,
				Original: len(m.Content), Outdated: true,
			}, "")
		}
		return ""
	}
	if len(m.Content) <= compressMinContentLen || strings.Contains(m.Content, "<<media:") {
		return ""
	}
	return softTrimWithHint(m.Content, m.Name, m.ID)
}

// computeWakeCompressed returns the Compressed value for a user message with wake YAML frontmatter.
// Strips redundant fields (thread/session/delivery/action) and compresses large assistant-only bodies.
// Returns "" if no compression is needed.
func computeWakeCompressed(m *provider.Message) string {
	yamlBlock, body, ok := splitFrontmatter(m.Content)
	if !ok {
		return ""
	}

	// Build trimmed YAML lines (remove redundant fields).
	var kept []string
	for _, line := range strings.Split(yamlBlock, "\n") {
		key := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
		if wakeTrimKeys[key] {
			continue
		}
		kept = append(kept, line)
	}
	trimmedYAML := strings.Join(kept, "\n")

	// Check whether body needs compression.
	visibility := extractFrontmatterValue(yamlBlock, "visibility")
	bodyLarge := visibility == "assistant-only" &&
		len(body) > compressMinContentLen &&
		!strings.Contains(body, "<<media:")

	if bodyLarge {
		n := len(body)
		hint := buildRecoveryHint(m.ID)
		var compressedBody string
		trimmed := 0
		if n > softTrimHeadChars+softTrimTailChars {
			trimmed = n - softTrimHeadChars - softTrimTailChars
			compressedBody = body[:softTrimHeadChars] + "\n\n" + hint + "\n\n" + body[n-softTrimTailChars:]
		} else {
			compressedBody = hint
		}
		// Append compression metadata inline into the wake YAML.
		trimmedYAML += "\ncompressed: true"
		trimmedYAML += fmt.Sprintf("\noriginal: %d", n)
		if trimmed > 0 {
			trimmedYAML += fmt.Sprintf("\ntrimmed: %d", trimmed)
		}
		return "---\n" + trimmedYAML + "\n---\n" + compressedBody
	}

	// Wake trim only — strip redundant fields but preserve full body.
	rebuilt := "---\n" + trimmedYAML + "\n---\n" + body
	if rebuilt == m.Content {
		return "" // nothing changed, no compression needed
	}
	return rebuilt
}

// wakeTrimKeys are the wake YAML frontmatter fields stripped from older messages.
var wakeTrimKeys = map[string]bool{
	"thread": true, "session": true, "delivery": true, "action": true,
}

// splitFrontmatter splits a YAML-frontmatter-wrapped string into its YAML block and body.
func splitFrontmatter(content string) (yamlBlock, body string, ok bool) {
	if !strings.HasPrefix(content, "---\n") {
		return
	}
	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx < 0 {
		return
	}
	endIdx += 4
	yamlBlock = content[4:endIdx]
	body = content[endIdx+5:] // skip "\n---\n"
	ok = true
	return
}

// extractFrontmatterValue extracts a scalar value from a raw YAML block by key name.
func extractFrontmatterValue(yamlBlock, key string) string {
	for _, line := range strings.Split(yamlBlock, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

// buildRecoveryHint builds the hint string pointing to the original content.
func buildRecoveryHint(messageID string) string {
	if messageID == "" {
		return compressedHintNoID
	}
	return fmt.Sprintf(compressedHintFmt, messageID)
}

// softTrimWithHint applies head+hint+tail compression and returns a Compressed value.
func softTrimWithHint(content, name, messageID string) string {
	n := len(content)
	hint := buildRecoveryHint(messageID)
	if n > softTrimHeadChars+softTrimTailChars {
		head := content[:softTrimHeadChars]
		tail := content[n-softTrimTailChars:]
		trimmed := n - softTrimHeadChars - softTrimTailChars
		return marshalCompressed(compressedHeader{
			Compressed: name, Original: n, Trimmed: trimmed,
		}, head+"\n\n"+hint+"\n\n"+tail)
	}
	// Large enough to compress but not enough to soft-trim: keep full content + hint.
	return marshalCompressed(compressedHeader{
		Compressed: name, Original: n,
	}, content+"\n\n"+hint)
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
