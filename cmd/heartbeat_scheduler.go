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
	hbScanInterval   = 30 * time.Second
	hbQuietMin       = 10 * time.Minute // User must be quiet for at least this long.
	hbPulseInterval  = 30 * time.Minute // Default minimum gap between pulses.
	hbFastPulse      = 10 * time.Minute // Gap when heartbeat.md was modified last turn.
	hbActivityWindow = 48 * time.Hour   // Only pulse sessions active within this window.
)

// heartbeatScheduler fires heartbeat pulses into user sessions on a fixed interval.
type heartbeatScheduler struct {
	mgr         *thread.Manager
	cfgFn       func() *config.Config
	sessionsDir string

	mu          sync.Mutex
	lastPulse   map[string]time.Time // sessionKey → last pulse time
	lastHBMtime map[string]time.Time // sessionKey → heartbeat.md mtime at last pulse
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

	postponed := loadPostponeConfig(filepath.Join(workspace, "system", "heartbeat-postpone.json"))

	// Collect candidate sessions + thread state in one pass.
	threadList := s.mgr.ListThreads()
	threadState := make(map[string]string, len(threadList)) // key → state
	candidates := make(map[string]time.Time)

	for _, ti := range threadList {
		threadState[ti.SessionKey] = ti.State
		if !ti.LastUserActiveAt.IsZero() {
			candidates[ti.SessionKey] = ti.LastUserActiveAt
		}
	}

	// Add on-disk GC'd sessions not already in memory.
	for _, key := range scanSessionDirs(s.sessionsDir) {
		if _, ok := candidates[key]; ok {
			continue
		}
		sessionDir := hbSessionKeyToDir(s.sessionsDir, key)
		lastActive := scanLastUserActive(sessionDir)
		if !lastActive.IsZero() && now.Sub(lastActive) <= hbActivityWindow {
			candidates[key] = lastActive
		}
	}

	// Clean up stale map entries.
	s.mu.Lock()
	for key := range s.lastPulse {
		if _, ok := candidates[key]; !ok {
			delete(s.lastPulse, key)
			delete(s.lastHBMtime, key)
		}
	}
	s.mu.Unlock()

	for key, lastActive := range candidates {
		if ctx.Err() != nil {
			return
		}
		if now.Sub(lastActive) < hbQuietMin {
			continue
		}
		if now.Sub(lastActive) > hbActivityWindow {
			continue
		}
		if until, ok := postponed[key]; ok {
			if t, err := time.Parse(time.RFC3339, until); err == nil && now.Before(t) {
				continue
			}
		}
		if state := threadState[key]; state != "" && state != "idle" {
			continue
		}

		s.maybeFirePulse(key, now, lastActive)
	}
}

func (s *heartbeatScheduler) maybeFirePulse(key string, now time.Time, lastActive time.Time) {
	sessionDir := hbSessionKeyToDir(s.sessionsDir, key)
	hbPath := filepath.Join(sessionDir, "heartbeat.md")
	hbMtime := hbFileMtime(hbPath)

	s.mu.Lock()
	prevMtime := s.lastHBMtime[key]
	lp := s.lastPulse[key]
	s.mu.Unlock()

	// Determine interval: fast if heartbeat.md was modified since last pulse.
	interval := hbPulseInterval
	if !prevMtime.IsZero() && !hbMtime.IsZero() && hbMtime.After(prevMtime) {
		interval = hbFastPulse
	}

	// On restart (lp is zero), align to user's last active time.
	if lp.IsZero() {
		lp = lastActive
	}
	if now.Sub(lp) < interval {
		return
	}

	// Read heartbeat.md content.
	content := ""
	if data, err := os.ReadFile(hbPath); err == nil {
		content = strings.TrimSpace(string(data))
	}

	nextPulse := now.Add(interval).UTC().Format(time.RFC3339)
	mdModified := ""
	if !hbMtime.IsZero() {
		mdModified = hbMtime.UTC().Format(time.RFC3339)
	}

	message := buildHeartbeatMessage(content, mdModified, nextPulse)

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
			key := ch.Name() + ":" + sid.Name()
			// Skip non-user sessions.
			if strings.HasPrefix(key, "cron:") {
				continue
			}
			keys = append(keys, key)
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

// buildHeartbeatMessage constructs a heartbeat system message.
func buildHeartbeatMessage(heartbeatContent, mdModified, nextPulse string) string {
	fields := map[string]string{}
	if nextPulse != "" {
		fields["next_pulse"] = nextPulse
	}
	if mdModified != "" {
		fields["heartbeat_modified"] = mdModified
	}

	body := "[heartbeat.md is empty]"
	if c := strings.TrimSpace(heartbeatContent); c != "" {
		body = c
	}

	message := sysmsg.BuildSystemMessage("heartbeat", fields, body)
	message += "\n\nYou must call use_skill(\"heartbeat-wake\") and follow its instructions."
	return message
}
