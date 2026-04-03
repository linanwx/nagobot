package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
	"gopkg.in/yaml.v3"
)

const (
	compressKeepAssistants = 3    // protect last N assistant turns
	softTrimHeadRunes      = 1500 // runes kept from start of result
	softTrimTailRunes      = 1500 // runes kept from end of result

	compressedHintFmt  = "[compressed — use search-memory --context %s --full to see content if needed, use skill session-ops to see more]"
	compressedHintNoID = "[compressed — use search-memory with session key and timeframe to find original content, or use skill session-ops to see more]"

	compressExpireAge      = 2 * time.Hour // unified age threshold for tier-1 compression
)

// runeHead returns the first n runes of s. If s has fewer than n runes, returns s unchanged.
func runeHead(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// runeTail returns the last n runes of s. If s has fewer than n runes, returns s unchanged.
func runeTail(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

// runeLen returns the number of runes in s.
func runeLen(s string) int {
	return len([]rune(s))
}


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
		idle := now.Sub(t.lastUserActiveAt)
		if t.state == threadIdle && idle >= tier1IdleMin {
			candidates = append(candidates, candidate{key: key, idle: idle})
		}
	}
	m.mu.Unlock()

	if len(candidates) == 0 {
		return
	}

	for _, c := range candidates {
		// Tier 1 always runs first (mechanical, idempotent, cheap).
		m.tryTier1Compress(c.key)
		// Tier 2 runs additionally when idle long enough and tokens exceed threshold.
		if c.idle >= tier2IdleMin {
			m.tryTier2Compress(c.key)
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

	// Capture thread state under lock, then release for expensive token estimation.
	m.mu.Lock()
	t, ok := m.threads[sessionKey]
	if !ok || t.state != threadIdle {
		m.mu.Unlock()
		return
	}
	toolDefs := t.tools.Defs()
	_, modelName := t.resolvedProviderModel()
	m.mu.Unlock()

	tokens := EstimateMessagesTokens(ApplyCompressed(sess.Messages)) + EstimateToolDefsTokens(toolDefs)
	effectiveWindow := provider.EffectiveContextWindow(modelName, cfg.ContextWindowTokens)
	ct := ComputeContextThresholds(effectiveWindow)
	threshold := effectiveWindow - ct.Tier2Token
	if tokens < threshold {
		return
	}

	// Re-acquire lock for cooldown checks and state mutation.
	m.mu.Lock()
	if t.state != threadIdle {
		m.mu.Unlock()
		return
	}
	// Skip if compression succeeded recently (10 min cooldown).
	if !t.lastCompressedAt.IsZero() && time.Since(t.lastCompressedAt) < 10*time.Minute {
		m.mu.Unlock()
		return
	}
	// Skip if an attempt was enqueued recently (2 min cooldown to avoid duplicate enqueue).
	if !t.lastCompressAttemptAt.IsZero() && time.Since(t.lastCompressAttemptAt) < 2*time.Minute {
		m.mu.Unlock()
		return
	}
	t.lastCompressAttemptAt = time.Now()
	m.mu.Unlock()

	sessionPath := cfg.Sessions.PathForKey(sessionKey)
	instruction := msg.BuildSystemMessage("compression_maintenance", map[string]string{
		"session_key":      sessionKey,
		"session_file":     sessionPath,
		"estimated_tokens": fmt.Sprintf("%d", tokens),
		"context_window":   fmt.Sprintf("%d", effectiveWindow),
		"usage_ratio":      fmt.Sprintf("%.2f", float64(tokens)/float64(effectiveWindow)),
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

		// Skip messages already marked by heartbeat trim pass.
		if m.HeartbeatTrim || (m.Role == "user" && strings.HasPrefix(m.Compressed, "[heartbeat_")) {
			continue
		}

		var newCompressed string

		switch m.Role {
		case "tool":
			newCompressed = computeToolCompressed(m, i, lastSkillLoad)
		case "user":
			if strings.HasPrefix(m.Content, "---\n") {
				newCompressed = computeWakeCompressed(m)
			}
		case "assistant":
			// Mark old reasoning for send-time exclusion (original data preserved).
			// Uses its own flag (ReasoningTrimmed) instead of Compressed, so skip
			// the newCompressed check below to avoid accidentally clearing Compressed.
			if !m.ReasoningTrimmed &&
				(m.ReasoningContent != "" || len(m.ReasoningDetails) > 0) &&
				!m.Timestamp.IsZero() && time.Since(m.Timestamp) > compressExpireAge {
				m.ReasoningTrimmed = true
				modified = true
			}
			continue
		}

		if newCompressed != m.Compressed {
			m.Compressed = newCompressed
			modified = true
		}
	}

	// Mark entire heartbeat turns for send-time removal.
	// Independent of protectFrom — heartbeat noise is never worth protecting.
	// Time-gated by compressExpireAge inside markHeartbeatTurns.
	if markHeartbeatTurns(result) {
		modified = true
	}

	return modified, result
}

// markHeartbeatTurns scans for heartbeat turns that can be collapsed to markers.
// A "turn" = user message + all subsequent assistant/tool messages until next user msg.
// No protectFrom boundary and no time gate — heartbeat turns are trimmed immediately
// since users never see them and leaving them risks the AI treating user messages
// as responses to heartbeat actions.
// Returns true if any messages were newly marked.
func markHeartbeatTurns(messages []provider.Message) bool {
	modified := false
	i := 0
	for i < len(messages) {
		if messages[i].Role != "user" {
			i++
			continue
		}

		source := messages[i].Source
		if !strings.HasPrefix(source, "heartbeat") {
			i++
			continue
		}

		// Find turn boundary: next user message or end of slice.
		turnEnd := i + 1
		for turnEnd < len(messages) && messages[turnEnd].Role != "user" {
			turnEnd++
		}

		shouldTrim := isHeartbeatSkipTurn(messages[i:turnEnd])
		trimType := "heartbeat"

		if !shouldTrim {
			i = turnEnd
			continue
		}

		// User message: set Compressed to a short marker.
		ts := messages[i].Timestamp.Format("15:04")
		marker := fmt.Sprintf("[heartbeat_%s at %s — trimmed]", trimType, ts)
		if marker != messages[i].Compressed {
			messages[i].Compressed = marker
			modified = true
		}

		// Assistant/tool messages: mark for send-time removal.
		for j := i + 1; j < turnEnd; j++ {
			if !messages[j].HeartbeatTrim {
				messages[j].HeartbeatTrim = true
				modified = true
			}
		}

		i = turnEnd
	}
	return modified
}

// isHeartbeatSkipTurn returns true if a heartbeat turn should be trimmed.
// A turn is trimmed if the AI called sleep_thread (chose to stay silent) or
// output SLEEP_THREAD_OK without tool calls (weak-model fallback).
// If the AI chose silence, the turn is noise — any valuable findings should
// already be persisted to heartbeat.md by the reflect/act skills.
func isHeartbeatSkipTurn(turnMessages []provider.Message) bool {
	endedSilently := false // sleep_thread called or SLEEP_THREAD_OK fallback
	for i := range turnMessages {
		m := &turnMessages[i]
		if m.Role == "tool" && m.Name == "sleep_thread" {
			endedSilently = true
		}
		if m.Role == "assistant" && len(m.ToolCalls) == 0 && strings.Contains(m.Content, "SLEEP_THREAD_OK") {
			endedSilently = true
		}
	}
	return endedSilently
}

// computeToolCompressed returns the Compressed value for a tool message.
// Returns "" if no compression is needed.
func computeToolCompressed(m *provider.Message, idx int, lastSkillLoad map[string]int) string {
	if m.Name == "use_skill" {
		skillName := extractSkillName(m.Content)
		if skillName == "" {
			return ""
		}
		// Outdated: same skill loaded again later → header-only, no hint
		if lastSkillLoad[skillName] > idx {
			return marshalCompressed(compressedHeader{
				Compressed: "use_skill", Skill: skillName,
				Original: len(m.Content), Outdated: true,
			}, "")
		}
		// Expired: older than compressExpireAge → header-only, with reload hint
		if !m.Timestamp.IsZero() && time.Since(m.Timestamp) > compressExpireAge {
			return marshalCompressed(compressedHeader{
				Compressed: "use_skill", Skill: skillName,
				Original: len(m.Content), Outdated: true,
			}, "[compressed — call use_skill to reload if needed]")
		}
		return ""
	}
	// Never compress compression results — they ARE the session summary.
	// Uses Contains because skip_trim lives inside a nested YAML block
	// (compress-session output wrapped by exec tool header).
	if strings.Contains(m.Content, "\nskip_trim: true\n") {
		return ""
	}

	// Expired: stale tool results → header-only after compressExpireAge.
	// read_file content may be outdated; web results are certainly stale.
	if expiredToolHeaderOnly[m.Name] && !m.Timestamp.IsZero() && time.Since(m.Timestamp) > compressExpireAge {
		return marshalCompressed(compressedHeader{
			Compressed: m.Name, Original: len(m.Content), Outdated: true,
		}, "[compressed — call "+m.Name+" again to get fresh content if needed]")
	}
	if runeLen(m.Content) <= softTrimHeadRunes+softTrimTailRunes || strings.Contains(m.Content, "<<media:") {
		return ""
	}
	return softTrimWithHint(m.Content, m.Name, m.ID)
}

// expiredToolHeaderOnly lists tools whose results should be compressed to
// header-only after compressExpireAge (2h). These tools return content that
// becomes stale over time (file may change, web results outdated).
var expiredToolHeaderOnly = map[string]bool{
	"read_file":  true,
	"web_fetch":  true,
	"web_search": true,
	"grep":       true,
	"health":     true,
}

// computeWakeCompressed returns the Compressed value for a user message with wake YAML frontmatter.
// Strips redundant fields (thread/session/delivery/action) and compresses large system-sender bodies.
// Returns "" if no compression is needed.
func computeWakeCompressed(m *provider.Message) string {
	yamlBlock, body, ok := SplitFrontmatter(m.Content)
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

	// Check whether body needs compression: only when actually trimmable.
	sender := ExtractFrontmatterValue(yamlBlock, "sender")
	bodyRuneLen := runeLen(body)
	bodyTrimmable := sender == "system" &&
		bodyRuneLen > softTrimHeadRunes+softTrimTailRunes &&
		!strings.Contains(body, "<<media:")

	if bodyTrimmable {
		n := bodyRuneLen
		trimmed := n - softTrimHeadRunes - softTrimTailRunes
		hint := buildRecoveryHint(m.ID)
		compressedBody := runeHead(body, softTrimHeadRunes) + "\n\n" + hint + "\n\n" + runeTail(body, softTrimTailRunes)
		bodyYAML := trimmedYAML + "\ncompressed: true"
		bodyYAML += fmt.Sprintf("\noriginal: %d", n)
		bodyYAML += fmt.Sprintf("\ntrimmed: %d", trimmed)
		result := "---\n" + bodyYAML + "\n---\n" + compressedBody
		// Skip body trim if result is not at least 5% smaller than original.
		if len(result) < int(float64(len(m.Content))*0.95) {
			return result
		}
		// Fall through to wake trim only (strip redundant YAML fields).
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
	"thread": true, "session": true, "session_dir": true, "delivery": true, "action": true, "agent": true,
}

// SplitFrontmatter splits a YAML-frontmatter-wrapped string into its YAML block and body.
func SplitFrontmatter(content string) (yamlBlock, body string, ok bool) {
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

// ExtractFrontmatterValue extracts a scalar value from a raw YAML block by key name.
func ExtractFrontmatterValue(yamlBlock, key string) string {
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
// Only compresses when the result is at least 5% smaller than the original.
func softTrimWithHint(content, name, messageID string) string {
	n := runeLen(content)
	if n <= softTrimHeadRunes+softTrimTailRunes {
		return ""
	}
	head := runeHead(content, softTrimHeadRunes)
	tail := runeTail(content, softTrimTailRunes)
	trimmed := n - softTrimHeadRunes - softTrimTailRunes
	hint := buildRecoveryHint(messageID)
	result := marshalCompressed(compressedHeader{
		Compressed: name, Original: n, Trimmed: trimmed,
	}, head+"\n\n"+hint+"\n\n"+tail)
	// Skip if compression didn't shrink by at least 5%.
	if len(result) >= int(float64(len(content))*0.95) {
		return ""
	}
	return result
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
