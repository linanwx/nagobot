package health

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxRecentEntries = 5
	logTimePrefix    = "time="
	logLevelWarn     = "level=WARN"
	logLevelError    = "level=ERROR"
)

// scanLogs scans log files in logsDir for WARN/ERROR entries in the last 24h.
// Returns nil if logsDir is empty or inaccessible.
func scanLogs(logsDir string) *LogHealth {
	if logsDir == "" {
		return nil
	}

	cutoff := time.Now().Add(-24 * time.Hour)

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil
	}

	lh := &LogHealth{}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		scanLogFile(filepath.Join(logsDir, name), cutoff, lh)
	}

	if lh.WarnCount == 0 && lh.ErrorCount == 0 {
		return nil
	}
	return lh
}

// scanLogFile scans a single log file line-by-line, collecting
// warn/error entries that occurred after cutoff.
func scanLogFile(path string, cutoff time.Time, lh *LogHealth) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Increase buffer for potentially long log lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Quick filter: must contain WARN or ERROR.
		isWarn := strings.Contains(line, logLevelWarn)
		isError := strings.Contains(line, logLevelError)
		if !isWarn && !isError {
			continue
		}

		// Parse timestamp to filter by cutoff.
		ts := parseLogTime(line)
		if ts.IsZero() || ts.Before(cutoff) {
			continue
		}

		if isError {
			lh.ErrorCount++
			appendRecent(&lh.RecentErrors, line)
		} else {
			lh.WarnCount++
			appendRecent(&lh.RecentWarnings, line)
		}
	}
}

// parseLogTime extracts the timestamp from a slog text log line.
// Expected format: time=2026-03-22T10:30:00.000Z ...
// or:              time=2026-03-22T10:30:00.000+08:00 ...
func parseLogTime(line string) time.Time {
	idx := strings.Index(line, logTimePrefix)
	if idx < 0 {
		return time.Time{}
	}
	rest := line[idx+len(logTimePrefix):]

	// Find the end of the time value (next space or end of line).
	end := strings.IndexByte(rest, ' ')
	if end < 0 {
		end = len(rest)
	}
	raw := rest[:end]

	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		// Try RFC3339 without nanos.
		t, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

// appendRecent keeps only the last maxRecentEntries entries in the slice.
func appendRecent(dst *[]string, line string) {
	if len(*dst) >= maxRecentEntries {
		// Shift left to make room for the new entry.
		copy((*dst)[0:], (*dst)[1:])
		(*dst)[maxRecentEntries-1] = line
	} else {
		*dst = append(*dst, line)
	}
}
