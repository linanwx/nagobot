package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/spf13/cobra"
)

var (
	searchMemoryDays    int
	searchMemoryLimit   int
	searchMemorySession string
	searchMemoryAfter   string
	searchMemoryBefore  string
	searchMemoryContext string
	searchMemoryWindow  int
)

var searchMemoryCmd = &cobra.Command{
	Use:   "search-memory <keywords...>",
	Short: "Search across all session messages and memory files",
	Long: `Search conversation history across all sessions by merging current session
and history backups, deduplicating by message ID.

Two modes:
  1. Keyword search (default): search-memory <keyword1> [keyword2...]
  2. Context browse: search-memory --context <message-id> [--window N]`,
	GroupID: "internal",
	RunE:   runSearchMemory,
}

func init() {
	searchMemoryCmd.Flags().IntVar(&searchMemoryDays, "days", 30, "Only search sessions active within N days")
	searchMemoryCmd.Flags().IntVar(&searchMemoryLimit, "limit", 20, "Maximum number of results")
	searchMemoryCmd.Flags().StringVar(&searchMemorySession, "session", "", "Limit search to a specific session key")
	searchMemoryCmd.Flags().StringVar(&searchMemoryAfter, "after", "", "Only include messages after this date (YYYY-MM-DD or RFC3339)")
	searchMemoryCmd.Flags().StringVar(&searchMemoryBefore, "before", "", "Only include messages before this date (YYYY-MM-DD or RFC3339)")
	searchMemoryCmd.Flags().StringVar(&searchMemoryContext, "context", "", "Browse messages around a specific message ID")
	searchMemoryCmd.Flags().IntVar(&searchMemoryWindow, "window", 5, "Number of messages before and after the target (used with --context)")
	rootCmd.AddCommand(searchMemoryCmd)
}

type searchHit struct {
	SessionKey string `json:"session_key"`
	MessageID  string `json:"message_id"`
	Role       string `json:"role"`
	Timestamp  string `json:"timestamp"`
	Snippet    string `json:"snippet"`
	Score      int    `json:"score"`
}

type searchOutput struct {
	Query   string      `json:"query"`
	Hits    []searchHit `json:"hits"`
	Total   int         `json:"total"`
	Shown   int         `json:"shown"`
	Scanned int         `json:"scanned"`
}

func runSearchMemory(_ *cobra.Command, args []string) error {
	if searchMemoryContext != "" {
		return runContextBrowse(args)
	}
	if len(args) == 0 {
		return fmt.Errorf("at least one keyword is required (or use --context <message-id>)")
	}
	return runKeywordSearch(args)
}

func runKeywordSearch(args []string) error {
	keywords := make([]string, len(args))
	for i, a := range args {
		keywords[i] = strings.ToLower(a)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return fmt.Errorf("failed to get sessions dir: %w", err)
	}
	cutoff := time.Now().AddDate(0, 0, -searchMemoryDays)

	// Parse --after / --before time filters.
	var afterTime, beforeTime time.Time
	if searchMemoryAfter != "" {
		if t, err := parseFlexibleTime(searchMemoryAfter); err != nil {
			return fmt.Errorf("invalid --after: %w", err)
		} else {
			afterTime = t
		}
	}
	if searchMemoryBefore != "" {
		if t, err := parseFlexibleTime(searchMemoryBefore); err != nil {
			return fmt.Errorf("invalid --before: %w", err)
		} else {
			beforeTime = t
		}
	}

	// Collect all session directories.
	type sessionDir struct {
		key string
		dir string
	}
	var dirs []sessionDir

	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || d.Name() != session.SessionFileName {
			return nil
		}
		dir := filepath.Dir(path)
		key := deriveSessionKey(sessionsDir, path)

		if searchMemorySession != "" && key != searchMemorySession {
			return nil
		}

		// Check recency via last message timestamp.
		if ts, err := session.ReadUpdatedAt(path); err != nil || ts.IsZero() || ts.Before(cutoff) {
			return nil
		}

		dirs = append(dirs, sessionDir{key: key, dir: dir})
		return nil
	})

	// Merge messages from session + history, dedup by ID.
	var allHits []searchHit
	scanned := 0

	for _, sd := range dirs {
		merged := mergeSessionMessages(sd.dir)
		for _, m := range merged {
			scanned++
			if m.Content == "" {
				continue
			}
			// Time range filter on message timestamp.
			hasTimeFilter := !afterTime.IsZero() || !beforeTime.IsZero()
			if hasTimeFilter && m.Timestamp.IsZero() {
				continue // Exclude messages without timestamps when time range is specified.
			}
			if !afterTime.IsZero() && m.Timestamp.Before(afterTime) {
				continue
			}
			if !beforeTime.IsZero() && m.Timestamp.After(beforeTime) {
				continue
			}
			score := scoreMessage(m.Content, keywords)
			if score == 0 {
				continue
			}
			snippet := extractSnippet(m.Content, keywords, 200)
			ts := ""
			if !m.Timestamp.IsZero() {
				ts = m.Timestamp.Format(time.RFC3339)
			}
			allHits = append(allHits, searchHit{
				SessionKey: sd.key,
				MessageID:  m.ID,
				Role:       m.Role,
				Timestamp:  ts,
				Snippet:    snippet,
				Score:      score,
			})
		}
	}

	sort.Slice(allHits, func(i, j int) bool {
		return allHits[i].Score > allHits[j].Score
	})

	shown := allHits
	if len(shown) > searchMemoryLimit {
		shown = shown[:searchMemoryLimit]
	}

	output := searchOutput{
		Query:   strings.Join(args, " "),
		Hits:    shown,
		Total:   len(allHits),
		Shown:   len(shown),
		Scanned: scanned,
	}
	if output.Hits == nil {
		output.Hits = []searchHit{}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

type contextMessage struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp,omitempty"`
	Content   string `json:"content"`
	IsTarget  bool   `json:"is_target,omitempty"`
}

type contextOutput struct {
	TargetID   string           `json:"target_id"`
	SessionKey string           `json:"session_key"`
	Messages   []contextMessage `json:"messages"`
	Position   int              `json:"position"`
	Total      int              `json:"total"`
}

func runContextBrowse(_ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return fmt.Errorf("failed to get sessions dir: %w", err)
	}

	// Derive session key from message ID.
	// Message ID format: sessionKey:unixMillis:seq (e.g. "telegram:5358956630:1772795279732:197")
	// Session key is everything except the last two colon-separated parts.
	sessionKey := deriveSessionKeyFromMsgID(searchMemoryContext)
	if sessionKey == "" {
		return fmt.Errorf("cannot derive session key from message ID %q", searchMemoryContext)
	}

	// Build session directory path.
	parts := strings.Split(sessionKey, ":")
	dirParts := append([]string{sessionsDir}, parts...)
	sessionDir := filepath.Join(dirParts...)

	merged := mergeSessionMessages(sessionDir)
	if len(merged) == 0 {
		return fmt.Errorf("no messages found for session %q", sessionKey)
	}

	// Find target message index.
	targetIdx := -1
	for i, m := range merged {
		if m.ID == searchMemoryContext {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		return fmt.Errorf("message ID %q not found in session %q", searchMemoryContext, sessionKey)
	}

	// Extract window.
	start := max(targetIdx-searchMemoryWindow, 0)
	end := min(targetIdx+searchMemoryWindow+1, len(merged))

	var msgs []contextMessage
	for i := start; i < end; i++ {
		m := merged[i]
		ts := ""
		if !m.Timestamp.IsZero() {
			ts = m.Timestamp.Format(time.RFC3339)
		}
		content := m.Content
		// Truncate very long messages for readability.
		if r := []rune(content); len(r) > 500 {
			content = string(r[:500]) + "..."
		}
		msgs = append(msgs, contextMessage{
			MessageID: m.ID,
			Role:      m.Role,
			Timestamp: ts,
			Content:   content,
			IsTarget:  i == targetIdx,
		})
	}

	output := contextOutput{
		TargetID:   searchMemoryContext,
		SessionKey: sessionKey,
		Messages:   msgs,
		Position:   targetIdx,
		Total:      len(merged),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// deriveSessionKeyFromMsgID extracts the session key from a message ID.
// Message ID: "telegram:5358956630:1772795279732:197" → session key: "telegram:5358956630"
// The last two colon-separated parts are unixMillis and seq.
func deriveSessionKeyFromMsgID(msgID string) string {
	parts := strings.Split(msgID, ":")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ":")
}

// mergeSessionMessages loads session.jsonl + all history/*.jsonl, deduplicates by message ID.
// For messages without an ID (legacy format), uses a content hash as dedup key.
func mergeSessionMessages(dir string) []provider.Message {
	seen := make(map[string]bool)
	var all []provider.Message

	addMessages := func(path string) {
		s, err := session.ReadFile(path)
		if err != nil {
			return
		}
		for _, m := range s.Messages {
			key := m.ID
			if key == "" {
				// Legacy messages without ID: dedup by role+content hash.
				key = "hash:" + contentHash(m.Role, m.Content)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, m)
		}
	}

	// Load history first (older), then current session (newer overrides if no ID).
	historyDir := filepath.Join(dir, "history")
	if entries, err := os.ReadDir(historyDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
				addMessages(filepath.Join(historyDir, e.Name()))
			}
		}
	}

	// Current session.
	addMessages(filepath.Join(dir, session.SessionFileName))

	return all
}

// contentHash returns a short hex hash for deduplicating legacy messages without IDs.
func contentHash(role, content string) string {
	var h uint64 = 14695981039346656037 // FNV-1a 64-bit offset basis
	for _, b := range []byte(role + "\x00" + content) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}

// scoreMessage returns a relevance score. 0 means no match (all keywords must match).
func scoreMessage(content string, keywords []string) int {
	lower := strings.ToLower(content)
	matched := 0
	totalCount := 0
	for _, kw := range keywords {
		count := strings.Count(lower, kw)
		if count > 0 {
			matched++
			totalCount += count
		}
	}
	if matched < len(keywords) {
		return 0 // AND: all keywords must match
	}
	return matched*10 + totalCount
}

// extractSnippet finds the best matching region around the first keyword match.
func extractSnippet(content string, keywords []string, maxLen int) string {
	lower := strings.ToLower(content)

	// Find earliest keyword position.
	bestPos := len(content)
	for _, kw := range keywords {
		pos := strings.Index(lower, kw)
		if pos >= 0 && pos < bestPos {
			bestPos = pos
		}
	}
	if bestPos == len(content) {
		bestPos = 0
	}

	// Extract window around match.
	runes := []rune(content)
	runeLen := len(runes)

	// Convert byte position to rune position.
	runePos := len([]rune(content[:bestPos]))
	start := max(runePos-maxLen/4, 0)
	end := start + maxLen
	if end > runeLen {
		end = runeLen
		start = max(end-maxLen, 0)
	}

	snippet := string(runes[start:end])
	// Collapse whitespace for readability.
	snippet = strings.Join(strings.Fields(snippet), " ")

	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "..."
	}
	if end < runeLen {
		suffix = "..."
	}
	return prefix + snippet + suffix
}

// parseFlexibleTime parses YYYY-MM-DD or RFC3339 format.
func parseFlexibleTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected YYYY-MM-DD or RFC3339, got %q", s)
}
