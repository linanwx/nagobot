// Package monitor provides metrics collection, storage, and balance checking.
package monitor

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
	metricsFileName = "turns.jsonl"
	retentionDays   = 7
)

// TurnRecord captures metrics for a single thread turn.
type TurnRecord struct {
	Timestamp        time.Time `json:"ts"`
	DurationMs       int64     `json:"durationMs"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	Agent            string    `json:"agent"`
	SessionKey       string    `json:"sessionKey"`
	Iterations       int       `json:"iterations"`
	ToolCalls        int       `json:"toolCalls"`
	PromptTokens     int       `json:"promptTokens"`
	CompletionTokens int       `json:"completionTokens"`
	TotalTokens      int       `json:"totalTokens"`
	CachedTokens     int       `json:"cachedTokens,omitempty"`
	ReasoningTokens  int       `json:"reasoningTokens,omitempty"`
	Error            bool      `json:"error,omitempty"`

	// Client-side token estimates for calibration.
	PromptEstimated    int `json:"promptEstimated,omitempty"`
	ReasoningEstimated int `json:"reasoningEstimated,omitempty"`
	MediaImageCount    int `json:"mediaImageCount,omitempty"`
	MediaImageEst      int `json:"mediaImageEst,omitempty"`
	MediaAudioCount    int `json:"mediaAudioCount,omitempty"`
	MediaAudioEst      int `json:"mediaAudioEst,omitempty"`
	MediaPDFCount      int `json:"mediaPDFCount,omitempty"`
	MediaPDFEst        int `json:"mediaPDFEst,omitempty"`
}

// Store persists and queries turn metrics.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore creates a metrics store at the given directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the metrics directory path.
func (s *Store) Dir() string { return s.dir }

// Record appends a turn record to the JSONL file.
func (s *Store) Record(r TurnRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		logger.Warn("monitor: failed to create metrics dir", "err", err)
		return
	}

	f, err := os.OpenFile(s.filePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn("monitor: failed to open metrics file", "err", err)
		return
	}
	defer f.Close()

	data, err := json.Marshal(r)
	if err != nil {
		logger.Warn("monitor: failed to marshal record", "err", err)
		return
	}
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		logger.Warn("monitor: failed to write record", "err", err)
	}
}

// Load reads all records from the JSONL file, optionally filtering by a cutoff time.
// Records older than cutoff are excluded. Pass time.Time{} to load all.
func (s *Store) Load(cutoff time.Time) []TurnRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(cutoff)
}

// loadLocked reads records without acquiring the mutex. Caller must hold s.mu.
func (s *Store) loadLocked(cutoff time.Time) []TurnRecord {
	f, err := os.Open(s.filePath())
	if err != nil {
		return nil
	}
	defer f.Close()

	var records []TurnRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var r TurnRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if !cutoff.IsZero() && r.Timestamp.Before(cutoff) {
			continue
		}
		records = append(records, r)
	}
	return records
}

// Rotate removes records older than retention period.
func (s *Store) Rotate() {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	s.mu.Lock()
	defer s.mu.Unlock()

	records := s.loadLocked(cutoff)

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return
	}

	f, err := os.Create(s.filePath())
	if err != nil {
		return
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			continue
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	w.Flush()
}

func (s *Store) filePath() string {
	return filepath.Join(s.dir, metricsFileName)
}
