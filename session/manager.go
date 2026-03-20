package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/provider"
)

// generateMessageID produces a unique, timestamp-ordered message ID.
// Format: sessionKey:unixMillis:hash (e.g. "telegram:123456:1709571234567:000001").
func generateMessageID(sessionKey string, ts time.Time, seq int) string {
	return fmt.Sprintf("%s:%d:%06d", sessionKey, ts.UnixMilli(), seq)
}

// EnsureMessageIDs assigns timestamps and IDs to messages that lack them.
// The sequence suffix is a content hash, so the same message always gets the same ID.
func EnsureMessageIDs(key string, messages []provider.Message) {
	now := time.Now()
	for i := range messages {
		if messages[i].Timestamp.IsZero() {
			messages[i].Timestamp = now
		}
		if messages[i].ID == "" {
			messages[i].ID = generateMessageID(key, messages[i].Timestamp, msgHash(messages[i]))
		}
	}
}

// msgHash returns a 0-999999 hash from message content for stable ID generation.
func msgHash(m provider.Message) int {
	var h uint32 = 2166136261 // FNV-1a offset basis
	for _, b := range []byte(m.Role + "\x00" + m.Content + "\x00" + m.ToolCallID) {
		h ^= uint32(b)
		h *= 16777619
	}
	return int(h % 1000000)
}

// Session represents a conversation session.
type Session struct {
	Key       string             `json:"key"`
	Messages  []provider.Message `json:"messages"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// Manager manages conversation sessions.
type Manager struct {
	sessionsDir string
	cache       map[string]*Session
	mu          sync.RWMutex
}

// NewManager creates a new session manager rooted at the given sessions directory.
func NewManager(sessionsDir string) (*Manager, error) {
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, err
	}
	return &Manager{
		sessionsDir: sessionsDir,
		cache:       make(map[string]*Session),
	}, nil
}

// Get returns a session by key, creating one if it doesn't exist.
func (m *Manager) Get(key string) (*Session, error) {
	key = normalizeSessionKey(key)

	m.mu.RLock()
	if s, ok := m.cache[key]; ok {
		m.mu.RUnlock()
		return s, nil
	}
	m.mu.RUnlock()

	s, err := m.loadFromDisk(key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if cached, ok := m.cache[key]; ok {
		m.mu.Unlock()
		return cached, nil
	}
	m.cache[key] = s
	m.mu.Unlock()
	return s, nil
}

// Reload forces loading session state from disk and refreshes cache.
func (m *Manager) Reload(key string) (*Session, error) {
	key = normalizeSessionKey(key)

	s, err := m.loadFromDisk(key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[key] = s
	m.mu.Unlock()
	return s, nil
}

// Save atomically rewrites the full session file (temp + rename).
// Used for compression and clear operations. For normal turns, use Append.
func (m *Manager) Save(s *Session) error {
	s.Key = normalizeSessionKey(s.Key)
	EnsureMessageIDs(s.Key, s.Messages)
	deriveTimestamps(s)
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now()
	}

	path := m.sessionPath(s.Key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := writeJSONL(f, s.Messages); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}

	// Update cache so concurrent Get() calls see the new state.
	m.mu.Lock()
	m.cache[s.Key] = s
	m.mu.Unlock()
	return nil
}

// Append persists new messages by appending to the session file.
// Creates the file if it doesn't exist. Updates the in-memory cache.
func (m *Manager) Append(key string, msgs ...provider.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	key = normalizeSessionKey(key)
	EnsureMessageIDs(key, msgs)

	path := m.sessionPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := writeJSONL(f, msgs); err != nil {
		return err
	}

	m.mu.Lock()
	if s, ok := m.cache[key]; ok {
		s.Messages = append(s.Messages, msgs...)
		if last := msgs[len(msgs)-1].Timestamp; !last.IsZero() {
			s.UpdatedAt = last
		}
	} else {
		// First append for this key — initialize cache entry.
		now := time.Now()
		ts := now
		if last := msgs[len(msgs)-1].Timestamp; !last.IsZero() {
			ts = last
		}
		m.cache[key] = &Session{
			Key:       key,
			Messages:  append([]provider.Message(nil), msgs...),
			CreatedAt: now,
			UpdatedAt: ts,
		}
	}
	m.mu.Unlock()

	return nil
}

func (m *Manager) sessionPath(key string) string {
	return filepath.Join(SessionDir(m.sessionsDir, key), SessionFileName)
}

// PathForKey returns the on-disk session file path for a session key.
func (m *Manager) PathForKey(key string) string {
	return m.sessionPath(key)
}

func (m *Manager) loadFromDisk(key string) (*Session, error) {
	key = normalizeSessionKey(key)

	path := m.sessionPath(key)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			now := time.Now()
			return &Session{
				Key:       key,
				Messages:  []provider.Message{},
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		}
		return nil, err
	}
	defer f.Close()

	messages, err := readJSONL(f)
	if err != nil {
		return nil, err
	}
	if messages == nil {
		messages = []provider.Message{}
	}
	messages = provider.SanitizeMessages(messages)

	s := &Session{
		Key:      key,
		Messages: messages,
	}
	deriveTimestamps(s)
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}

	EnsureMessageIDs(key, s.Messages)
	return s, nil
}

func normalizeSessionKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "cli"
	}
	return key
}

func sanitizePathSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(segment))
	lastUnderscore := false
	for _, r := range segment {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "_"
	}
	return out
}
