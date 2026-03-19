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
	hbScanInterval  = 30 * time.Second
	hbQuietMin      = 10 * time.Minute // User must be quiet for at least this long.
	hbPulseInterval = 30 * time.Minute // Default minimum gap between pulses.
	hbFastPulse     = 10 * time.Minute // Gap when heartbeat.md was modified last turn.
	hbActivityWindow = 48 * time.Hour  // Only pulse sessions active within this window.
)

// heartbeatScheduler fires heartbeat pulses into user sessions on a fixed interval.
type heartbeatScheduler struct {
	mgr    *thread.Manager
	cfgFn  func() *config.Config

	mu          sync.Mutex
	lastPulse   map[string]time.Time // sessionKey → last pulse time
	lastHBMtime map[string]time.Time // sessionKey → heartbeat.md mtime at last pulse
}

func newHeartbeatScheduler(mgr *thread.Manager, cfgFn func() *config.Config) *heartbeatScheduler {
	return &heartbeatScheduler{
		mgr:         mgr,
		cfgFn:       cfgFn,
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

	// Use the same session collection as list-sessions: loads session data,
	// scans for real user-visible messages, applies UserOnly filter.
	opts := listSessionsOpts{Days: 2, UserOnly: true}
	sessions, err := collectSessions(cfg, opts)
	if err != nil {
		logger.Warn("heartbeat scan: collectSessions failed", "err", err)
		return
	}

	// Enrich with live thread state (running/idle/pending).
	enrichWithThreads(sessions, s.mgr.ListThreads())

	// Clean up stale map entries.
	activeKeys := make(map[string]bool, len(sessions.Sessions))
	for _, se := range sessions.Sessions {
		activeKeys[se.Key] = true
	}
	s.mu.Lock()
	for key := range s.lastPulse {
		if !activeKeys[key] {
			delete(s.lastPulse, key)
			delete(s.lastHBMtime, key)
		}
	}
	s.mu.Unlock()

	for _, se := range sessions.Sessions {
		if ctx.Err() != nil {
			return
		}

		// LastUserActiveAt is from session.jsonl scan (real user-visible messages).
		if se.LastUserActiveAt == nil {
			continue
		}
		lastActive, parseErr := time.Parse(time.RFC3339, *se.LastUserActiveAt)
		if parseErr != nil {
			continue
		}

		if now.Sub(lastActive) < hbQuietMin {
			continue
		}
		if now.Sub(lastActive) > hbActivityWindow {
			continue
		}
		if until, ok := postponed[se.Key]; ok {
			if t, parseErr := time.Parse(time.RFC3339, until); parseErr == nil && now.Before(t) {
				continue
			}
		}
		if se.IsRunning {
			continue
		}

		sessionsDir, _ := cfg.SessionsDir()
		s.maybeFirePulse(se.Key, now, lastActive, sessionsDir)
	}
}

func (s *heartbeatScheduler) maybeFirePulse(key string, now time.Time, lastActive time.Time, sessionsDir string) {
	sessionDir := hbSessionKeyToDir(sessionsDir, key)
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

	if lp.IsZero() {
		// Pulse schedule: lastActive+10m, +40m, +70m, ...
		// Iterate to find the correct alignment point.
		firstPulse := lastActive.Add(hbQuietMin)
		if !firstPulse.Before(now) {
			lp = lastActive
			interval = hbQuietMin
		} else {
			next := firstPulse
			for next.Before(now) {
				next = next.Add(hbPulseInterval)
			}
			lp = next.Add(-hbPulseInterval)
			interval = hbPulseInterval
		}
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

	logger.Info("heartbeat pulse fired", "sessionKey", key, "nextPulse", nextPulse)
}

// hbSessionKeyToDir converts a session key to its directory path.
func hbSessionKeyToDir(sessionsDir, key string) string {
	parts := strings.Split(key, ":")
	return filepath.Join(append([]string{sessionsDir}, parts...)...)
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
		body = "## heartbeat.md\n\n" + c
	}

	message := sysmsg.BuildSystemMessage("heartbeat", fields, body)
	message += "\n\nYou must call use_skill(\"heartbeat-wake\") and follow its instructions."
	return message
}
