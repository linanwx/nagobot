package tools

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const (
	cleanupMaxAge   = 3 * 24 * time.Hour // delete files older than 3 days
	cleanupMaxFiles = 1000               // keep at most 1000 files
)

// CleanupLogsDir removes old tool call log files from dir.
// It first deletes files older than 3 days, then caps remaining files at 1000
// by removing the oldest. Safe to call with an empty or non-existent dir.
func CleanupLogsDir(dir string) {
	if dir == "" {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		logger.Warn("tool log cleanup: failed to read dir", "dir", dir, "err", err)
		return
	}

	if len(entries) == 0 {
		return
	}

	cutoff := time.Now().Add(-cleanupMaxAge)

	type fileEntry struct {
		name  string
		mtime time.Time
	}

	var remaining []fileEntry
	var removedAge int

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
				removedAge++
			}
		} else {
			remaining = append(remaining, fileEntry{name: e.Name(), mtime: info.ModTime()})
		}
	}

	// Cap at max files by removing oldest.
	var removedCap int
	if len(remaining) > cleanupMaxFiles {
		sort.Slice(remaining, func(i, j int) bool {
			return remaining[i].mtime.Before(remaining[j].mtime)
		})
		excess := remaining[:len(remaining)-cleanupMaxFiles]
		for _, f := range excess {
			if err := os.Remove(filepath.Join(dir, f.name)); err == nil {
				removedCap++
			}
		}
	}

	total := removedAge + removedCap
	if total > 0 {
		logger.Info("tool log cleanup", "removedAge", removedAge, "removedCap", removedCap, "total", total)
	}
}
