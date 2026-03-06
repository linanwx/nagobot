package monitor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const compressionFileName = "compressions.jsonl"

// CompressionRecord captures metrics when a session is compressed.
type CompressionRecord struct {
	Timestamp       time.Time      `json:"ts"`
	SessionKey      string         `json:"sessionKey"`
	MessagesBefore  int            `json:"messagesBefore"`
	MessagesAfter   int            `json:"messagesAfter"`
	EstimatedTokens int            `json:"estimatedTokens"`
	TotalChars      int            `json:"totalChars"`
	RoleCounts      map[string]int `json:"roleCounts"`
	MaxMsgChars     int            `json:"maxMsgChars"`
	MaxMsgRole      string         `json:"maxMsgRole"`
	MaxMsgPreview   string         `json:"maxMsgPreview"`
	AvgMsgChars     int            `json:"avgMsgChars"`
}

// RecordCompression appends a compression record to the JSONL file.
func (s *Store) RecordCompression(r CompressionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		logger.Warn("monitor: failed to create metrics dir", "err", err)
		return
	}

	f, err := os.OpenFile(s.compressionFilePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn("monitor: failed to open compression metrics file", "err", err)
		return
	}
	defer f.Close()

	data, err := json.Marshal(r)
	if err != nil {
		logger.Warn("monitor: failed to marshal compression record", "err", err)
		return
	}
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		logger.Warn("monitor: failed to write compression record", "err", err)
	}
}

// LoadCompressions reads all compression records, optionally filtered by cutoff.
func (s *Store) LoadCompressions(cutoff time.Time) []CompressionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.compressionFilePath())
	if err != nil {
		return nil
	}
	defer f.Close()

	var records []CompressionRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var r CompressionRecord
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

// FormatCompressionStats formats compression records into a readable summary.
func FormatCompressionStats(records []CompressionRecord) string {
	if len(records) == 0 {
		return "No compression records found."
	}

	var totalMsgsBefore, totalTokens int
	sessionCounts := map[string]int{}
	for _, r := range records {
		totalMsgsBefore += r.MessagesBefore
		totalTokens += r.EstimatedTokens
		sessionCounts[r.SessionKey]++
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Compression Stats (%d events):\n", len(records))
	fmt.Fprintf(&sb, "  Avg messages before compression: %d\n", totalMsgsBefore/len(records))
	fmt.Fprintf(&sb, "  Total tokens compressed:         %d\n", totalTokens)
	fmt.Fprintf(&sb, "  Sessions affected:               %d\n", len(sessionCounts))
	sb.WriteString("\nRecent compressions:\n")

	start := 0
	if len(records) > 10 {
		start = len(records) - 10
	}
	for _, r := range records[start:] {
		fmt.Fprintf(&sb, "  %s  %-30s  %d→%d msgs  ~%d tokens  max_msg=%d chars (%s)\n",
			r.Timestamp.Format("2006-01-02 15:04"),
			r.SessionKey,
			r.MessagesBefore, r.MessagesAfter,
			r.EstimatedTokens,
			r.MaxMsgChars, r.MaxMsgRole,
		)
		if r.MaxMsgPreview != "" {
			preview := r.MaxMsgPreview
			if len([]rune(preview)) > 80 {
				preview = string([]rune(preview)[:80]) + "..."
			}
			fmt.Fprintf(&sb, "    longest: \"%s\"\n", preview)
		}
	}
	return sb.String()
}

func (s *Store) compressionFilePath() string {
	return filepath.Join(s.dir, compressionFileName)
}
