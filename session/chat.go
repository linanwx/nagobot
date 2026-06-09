package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const chatFileName = "chat.jsonl"

// Roles recognized by chat.jsonl. The file is a clean user/assistant log used
// by lightweight helper agents (e.g. pre-think) and intentionally excludes
// tool calls, reasoning, and YAML wake frontmatter.
const (
	ChatRoleUser      = "user"
	ChatRoleAssistant = "assistant"
)

// ChatEntry is one line in chat.jsonl.
type ChatEntry struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	Ts      time.Time `json:"ts"`
}

// chatMutexes lazily-creates a per-sessionDir mutex so writes to the same
// chat.jsonl are serialized while writes to different sessions stay parallel.
// Without this, large entries (>PIPE_BUF) under POSIX O_APPEND could interleave.
var chatMutexes sync.Map

func chatMutexFor(dir string) *sync.Mutex {
	if m, ok := chatMutexes.Load(dir); ok {
		return m.(*sync.Mutex)
	}
	m, _ := chatMutexes.LoadOrStore(dir, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// AppendChat appends a single entry to {sessionDir}/chat.jsonl. Empty content
// is skipped. Callers should pass a non-zero ts; errors are returned for
// logging — chat-log failures must not fail the main flow.
func AppendChat(sessionDir, role, content string, ts time.Time) error {
	if sessionDir == "" {
		return fmt.Errorf("chat: empty sessionDir")
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}

	data, err := json.Marshal(ChatEntry{Role: role, Content: content, Ts: ts})
	if err != nil {
		return fmt.Errorf("chat: marshal entry: %w", err)
	}

	mu := chatMutexFor(sessionDir)
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("chat: mkdir %s: %w", sessionDir, err)
	}
	path := filepath.Join(sessionDir, chatFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("chat: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("chat: write %s: %w", path, err)
	}
	return nil
}

// Per-entry preview keeps the head AND tail of the content: the opening shows
// what the message is about, the closing keeps the conclusion/action — which a
// head-only truncation dropped for assistant messages (the part pre-think most
// needs to read intent).
const (
	chatPreviewHead = 200
	chatPreviewTail = 200
)

// ReadRecentChat returns up to n most-recent chat.jsonl entries from sessionDir,
// each rendered as "role: content" on a single line — newlines collapsed to
// spaces, content previewed as head + tail (first chatPreviewHead and last
// chatPreviewTail runes, middle elided with " [...] "). Returns "" if the file
// is missing or empty. Output is chronological (oldest of the N first).
func ReadRecentChat(sessionDir string, n int) string {
	if sessionDir == "" || n <= 0 {
		return ""
	}
	path := filepath.Join(sessionDir, chatFileName)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	truncated := false
	if len(lines) > n {
		lines = lines[len(lines)-n:]
		truncated = true
	}

	var b strings.Builder
	// When older entries were dropped, tell the reader so it doesn't mistake
	// the preview for the entire conversation.
	if truncated {
		b.WriteString("[... earlier conversation history has been collapsed; only the most recent messages are shown below ...]")
	}
	for _, line := range lines {
		var e ChatEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		content := collapseSpaces(e.Content)
		content = headTailRunes(content, chatPreviewHead, chatPreviewTail)
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Role)
		b.WriteString(": ")
		b.WriteString(content)
	}
	return b.String()
}

// collapseSpaces replaces any run of whitespace (including newlines) with a
// single space.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	out := b.String()
	return strings.TrimRight(out, " ")
}

// headTailRunes keeps the first head and last tail runes, eliding the middle
// with " [...] " when the content is longer than head+tail runes. Content that
// already fits is returned unchanged (no marker inserted).
func headTailRunes(s string, head, tail int) string {
	if head < 0 || tail < 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= head+tail {
		return s
	}
	return string(runes[:head]) + " [...] " + string(runes[len(runes)-tail:])
}
