package obs

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const (
	tracesFileName = "traces.jsonl"
	retentionDays  = 7
)

// SpanRecord is one line of traces.jsonl.
//
// The shape is chosen for an LLM reading the file, not for a UI: flat, short
// keys, one self-describing object per line, everything omitempty so a simple
// span stays short. At the measured volume the whole 7-day file is a few MB —
// small enough to read end to end, which is the entire point of writing it
// locally instead of shipping it to a trace backend.
//
// Ids stay full-length W3C (32/16 hex) rather than being shortened for
// readability: they are what makes the file joinable with a real trace backend
// if this is ever pointed at one, and analysis aggregates by Name anyway.
type SpanRecord struct {
	Timestamp time.Time      `json:"ts"`
	TraceID   string         `json:"trace"`
	SpanID    string         `json:"span"`
	ParentID  string         `json:"parent,omitempty"`
	Name      string         `json:"name"`
	DurMs     int64          `json:"dur_ms"`
	Status    string         `json:"status,omitempty"` // "error" only; ok is the default and omitted
	Error     string         `json:"err,omitempty"`
	Attrs     map[string]any `json:"attr,omitempty"`
	Links     []string       `json:"links,omitempty"` // trace ids of merged-in messages
}

// Store appends span records to a JSONL file and prunes it by age. Same shape
// and same retention as monitor.Store — deliberately a sibling rather than a
// shared abstraction, since the two records have nothing in common but the
// file format.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore creates a span store writing under dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Path returns the traces file path.
func (s *Store) Path() string { return filepath.Join(s.dir, tracesFileName) }

// Write appends a batch of span records in one file open. The exporter always
// hands over a batch, so per-span opens would be pure waste.
func (s *Store) Write(records []SpanRecord) error {
	if len(records) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			// One unmarshalable span must not drop the batch around it.
			logger.Warn("obs: failed to marshal span", "name", r.Name, "err", err)
			continue
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	return w.Flush()
}

// Rotate drops records older than the retention window. Called once at startup,
// mirroring the metrics store.
func (s *Store) Rotate() {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.Path())
	if err != nil {
		return
	}
	var kept [][]byte
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var r SpanRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if r.Timestamp.Before(cutoff) {
			continue
		}
		kept = append(kept, append([]byte(nil), scanner.Bytes()...))
	}
	f.Close()

	out, err := os.Create(s.Path())
	if err != nil {
		logger.Warn("obs: failed to rotate traces file", "err", err)
		return
	}
	defer out.Close()

	w := bufio.NewWriter(out)
	for _, line := range kept {
		w.Write(line)
		w.WriteByte('\n')
	}
	w.Flush()
}
