package session

import (
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
