package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/provider"
)

const maxLineSize = 1 << 20 // 1MB — handles large tool results

// readJSONL parses JSONL lines into messages.
// Malformed lines are skipped (crash recovery: truncated last line).
func readJSONL(r io.Reader) ([]provider.Message, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, maxLineSize), maxLineSize)

	var messages []provider.Message
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg provider.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			// Likely truncated last line from crash — skip it.
			continue
		}
		messages = append(messages, msg)
	}
	return messages, scanner.Err()
}

// writeJSONL writes messages as JSONL (one JSON object per line).
func writeJSONL(w io.Writer, msgs []provider.Message) error {
	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// ReadFile reads a session JSONL file and returns a Session.
// Messages are sanitized. Key is left empty — caller should set it.
// CreatedAt/UpdatedAt are derived from message timestamps.
func ReadFile(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
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

	s := &Session{Messages: messages}
	deriveTimestamps(s)
	return s, nil
}

// WriteFile atomically writes a session to a JSONL file (temp + rename).
func WriteFile(path string, s *Session) error {
	EnsureMessageIDs(s.Key, s.Messages)

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
	return os.Rename(tmp, path)
}

// deriveTimestamps sets CreatedAt/UpdatedAt from message timestamps.
func deriveTimestamps(s *Session) {
	if len(s.Messages) == 0 {
		return
	}
	if first := s.Messages[0].Timestamp; !first.IsZero() {
		s.CreatedAt = first
	}
	if last := s.Messages[len(s.Messages)-1].Timestamp; !last.IsZero() {
		s.UpdatedAt = last
	}
}

// ReadUpdatedAt returns the last message's timestamp from a session JSONL file.
// Much lighter than ReadFile — reads only the tail of the file without loading
// all messages or running sanitization.
func ReadUpdatedAt(path string) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return time.Time{}, err
	}
	if fi.Size() == 0 {
		return time.Time{}, nil
	}

	// Read tail of file to find last JSON line.
	readSize := fi.Size()
	if readSize > maxLineSize {
		readSize = maxLineSize
	}
	buf := make([]byte, readSize)
	if _, err := f.ReadAt(buf, fi.Size()-readSize); err != nil && err != io.EOF {
		return time.Time{}, err
	}

	// Find last complete line and extract timestamp.
	buf = bytes.TrimRight(buf, "\n\r")
	if idx := bytes.LastIndexByte(buf, '\n'); idx >= 0 {
		buf = buf[idx+1:]
	}

	var m struct {
		Timestamp time.Time `json:"timestamp"`
	}
	if err := json.Unmarshal(buf, &m); err != nil {
		return time.Time{}, nil // malformed last line
	}
	return m.Timestamp, nil
}

// SessionFileName is the canonical session file name.
const SessionFileName = "session.jsonl"

// SessionDir returns the on-disk directory for a session key.
// Applies the same normalization and sanitization as Manager.sessionPath.
func SessionDir(sessionsDir, key string) string {
	key = normalizeSessionKey(key)
	parts := strings.Split(key, ":")
	cleanParts := make([]string, 0, len(parts))
	for _, p := range parts {
		segment := sanitizePathSegment(p)
		if segment == "" {
			continue
		}
		cleanParts = append(cleanParts, segment)
	}
	if len(cleanParts) == 0 {
		cleanParts = append(cleanParts, "cli")
	}
	return filepath.Join(append([]string{sessionsDir}, cleanParts...)...)
}

// DeriveKeyFromPath extracts a session key from a file path.
// Given ".../sessions/telegram/12345/session.jsonl", returns "telegram:12345".
// Falls back to the parent directory name if "sessions" is not in the path.
func DeriveKeyFromPath(path string) string {
	dir := filepath.Dir(path) // strip session.jsonl
	parts := strings.Split(filepath.ToSlash(dir), "/")

	// Find "sessions" anchor and take everything after it.
	for i, p := range parts {
		if p == "sessions" && i+1 < len(parts) {
			return strings.Join(parts[i+1:], ":")
		}
	}

	// Fallback: use last directory component.
	base := filepath.Base(dir)
	if base == "." || base == "/" {
		return ""
	}
	return base
}
