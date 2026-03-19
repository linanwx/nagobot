package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
)

const (
	hbScanInterval    = 30 * time.Second
	hbQuietMin        = 10 * time.Minute  // User must be quiet for at least this long.
	hbPulseInterval   = 30 * time.Minute  // Default minimum gap between pulses.
	hbFastPulse       = 10 * time.Minute  // Gap when heartbeat.md was modified last turn.
	hbActivityWindow  = 48 * time.Hour    // Only pulse sessions active within this window.
)

// heartbeatScheduler fires heartbeat pulses into user sessions on a fixed interval.
type heartbeatScheduler struct {
	mgr        *thread.Manager
	cfgFn      func() *config.Config
	sessionsDir string

	mu           sync.Mutex
	lastPulse    map[string]time.Time // sessionKey → last pulse time
	lastHBMtime  map[string]time.Time // sessionKey → heartbeat.md mtime at last pulse
}

func newHeartbeatScheduler(mgr *thread.Manager, cfgFn func() *config.Config, sessionsDir string) *heartbeatScheduler {
	return &heartbeatScheduler{
		mgr:         mgr,
		cfgFn:       cfgFn,
		sessionsDir: sessionsDir,
		lastPulse:   make(map[string]time.Time),
		lastHBMtime: make(map[string]time.Time),
	}
}

func (s *heartbeatScheduler) run(ctx context.Context) {
	ticker := time.NewTicker(hbScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *heartbeatScheduler) scan(ctx context.Context) {
	now := time.Now()
	cfg := s.cfgFn()
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return
	}

	// Load postpone config.
	postponed := loadPostponeConfig(filepath.Join(workspace, "system", "heartbeat-postpone.json"))

	// Collect candidate sessions: in-memory threads + on-disk GC'd sessions.
	candidates := s.collectCandidates(now)

	for key, lastActive := range candidates {
		if err := ctx.Err(); err != nil {
			return
		}

		// Skip if user was active too recently.
		if now.Sub(lastActive) < hbQuietMin {
			continue
		}
		// Skip if no user activity within the activity window.
		if now.Sub(lastActive) > hbActivityWindow {
			continue
		}
		// Skip if session is postponed.
		if until, ok := postponed[key]; ok {
			if t, err := time.Parse(time.RFC3339, until); err == nil && now.Before(t) {
				continue
			}
		}
		// Skip if thread is currently running (it has pending work).
		if s.mgr.HasThread(key) {
			threads := s.mgr.ListThreads()
			for _, ti := range threads {
				if ti.SessionKey == key && ti.State != "idle" {
					goto nextSession
				}
			}
		}

		// Determine pulse interval: fast if heartbeat.md was modified during last turn.
		{
			sessionDir := hbSessionKeyToDir(s.sessionsDir, key)
			interval := hbPulseInterval

			s.mu.Lock()
			prevMtime, hasPrev := s.lastHBMtime[key]
			s.mu.Unlock()

			if hasPrev {
				currentMtime := hbFileMtime(filepath.Join(sessionDir, "heartbeat.md"))
				if !currentMtime.IsZero() && currentMtime.After(prevMtime) {
					interval = hbFastPulse
				}
			}

			s.mu.Lock()
			lp := s.lastPulse[key]
			s.mu.Unlock()

			if !lp.IsZero() && now.Sub(lp) < interval {
				continue
			}

			// Fire pulse.
			s.firePulse(key, sessionDir, now, interval)
		}
	nextSession:
	}
}

// collectCandidates returns sessionKey → lastUserActiveAt for all sessions.
func (s *heartbeatScheduler) collectCandidates(now time.Time) map[string]time.Time {
	candidates := make(map[string]time.Time)

	// 1. In-memory threads.
	for _, ti := range s.mgr.ListThreads() {
		if !ti.LastUserActiveAt.IsZero() {
			candidates[ti.SessionKey] = ti.LastUserActiveAt
		}
	}

	// 2. On-disk GC'd sessions (not already in memory).
	for _, key := range scanSessionDirs(s.sessionsDir) {
		if _, ok := candidates[key]; ok {
			continue // already have from in-memory thread
		}
		sessionDir := hbSessionKeyToDir(s.sessionsDir, key)
		lastActive := scanLastUserActive(sessionDir)
		if !lastActive.IsZero() && now.Sub(lastActive) <= hbActivityWindow {
			candidates[key] = lastActive
		}
	}

	return candidates
}

func (s *heartbeatScheduler) firePulse(key, sessionDir string, now time.Time, interval time.Duration) {
	// Read heartbeat.md content.
	body := "heartbeat pulse triggered"
	if data, err := os.ReadFile(filepath.Join(sessionDir, "heartbeat.md")); err == nil {
		if content := strings.TrimSpace(string(data)); content != "" {
			body = content
		}
	}

	// Record heartbeat.md mtime before pulsing.
	hbMtime := hbFileMtime(filepath.Join(sessionDir, "heartbeat.md"))

	nextPulse := now.Add(interval).UTC().Format(time.RFC3339)
	fields := map[string]string{
		"next_pulse": nextPulse,
	}
	if !hbMtime.IsZero() {
		fields["heartbeat_modified"] = hbMtime.UTC().Format(time.RFC3339)
	}

	message := sysmsg.BuildSystemMessage("heartbeat", fields, body)
	message += "\n\nYou must call use_skill(\"heartbeat-wake\") and follow its instructions."

	s.mgr.Wake(key, &thread.WakeMessage{
		Source:  thread.WakeHeartbeat,
		Message: message,
	})

	s.mu.Lock()
	s.lastPulse[key] = now
	s.lastHBMtime[key] = hbMtime
	s.mu.Unlock()

	logger.Debug("heartbeat pulse fired", "sessionKey", key, "nextPulse", nextPulse)
}

// hbSessionKeyToDir converts a session key to its directory path.
func hbSessionKeyToDir(sessionsDir, key string) string {
	parts := strings.Split(key, ":")
	return filepath.Join(append([]string{sessionsDir}, parts...)...)
}

// scanSessionDirs scans two levels under sessionsDir to find session directories.
// Skips directories named "threads" (child thread dirs).
func scanSessionDirs(sessionsDir string) []string {
	var keys []string
	channels, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}
	for _, ch := range channels {
		if !ch.IsDir() || ch.Name() == "threads" {
			continue
		}
		sessionIDs, err := os.ReadDir(filepath.Join(sessionsDir, ch.Name()))
		if err != nil {
			continue
		}
		for _, sid := range sessionIDs {
			if !sid.IsDir() || sid.Name() == "threads" {
				continue
			}
			keys = append(keys, ch.Name()+":"+sid.Name())
		}
	}
	return keys
}

// scanLastUserActive uses session.jsonl mtime as approximation of last user activity.
func scanLastUserActive(sessionDir string) time.Time {
	if fi, err := os.Stat(filepath.Join(sessionDir, "session.jsonl")); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// hbFileMtime returns the modification time of a file, or zero if it doesn't exist.
func hbFileMtime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// loadPostponeConfig reads heartbeat-postpone.json: map of sessionKey → RFC3339 until time.
func loadPostponeConfig(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

