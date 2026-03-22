package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
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
	logger.Debug("heartbeat scan started")
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
	logger.Debug("heartbeat scan: found sessions", "count", len(sessions.Sessions))

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
			logger.Debug("heartbeat skip: no user activity", "key", se.Key)
			continue
		}
		lastActive, parseErr := time.Parse(time.RFC3339, *se.LastUserActiveAt)
		if parseErr != nil {
			continue
		}

		quiet := now.Sub(lastActive)
		if quiet < hbQuietMin {
			logger.Debug("heartbeat skip: user active recently", "key", se.Key, "quiet", quiet.Round(time.Second))
			continue
		}
		if quiet > hbActivityWindow {
			logger.Debug("heartbeat skip: inactive >48h", "key", se.Key)
			continue
		}
		if entry, ok := postponed[se.Key]; ok {
			untilT, _ := time.Parse(time.RFC3339, entry.Until)
			createdT, _ := time.Parse(time.RFC3339, entry.CreatedAt)
			if now.Before(untilT) && !lastActive.After(createdT) {
				logger.Debug("heartbeat skip: postponed", "key", se.Key, "until", entry.Until)
				continue
			}
		}
		if se.IsRunning {
			logger.Debug("heartbeat skip: thread running", "key", se.Key)
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
		lp, interval = coldStartAlignment(lastActive, now)
		// Persist computed lp so subsequent scans don't re-compute and drift.
		s.mu.Lock()
		s.lastPulse[key] = lp
		s.mu.Unlock()
	}
	if now.Sub(lp) < interval {
		logger.Debug("heartbeat skip: interval not reached", "key", key,
			"lp", lp.Format(time.RFC3339), "interval", interval,
			"wait", (interval - now.Sub(lp)).Round(time.Second))
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

	message := buildHeartbeatMessage(content, mdModified, nextPulse, hbPath)

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

// hbStatusEntry represents one session's heartbeat status.
type hbStatusEntry struct {
	Key          string `json:"key"`
	LastActive   string `json:"last_active"`
	NextPulse    string `json:"next_pulse"`
	Status       string `json:"status"`
	HasHeartbeat bool   `json:"has_heartbeat"`
}

// Status returns the real heartbeat state for all eligible sessions,
// using the scheduler's actual in-memory lastPulse/lastHBMtime.
func (s *heartbeatScheduler) Status() []hbStatusEntry {
	now := time.Now()
	cfg := s.cfgFn()
	workspace, _ := cfg.WorkspacePath()
	postponed := loadPostponeConfig(filepath.Join(workspace, "system", "heartbeat-postpone.json"))

	opts := listSessionsOpts{Days: 2, UserOnly: true}
	sessions, err := collectSessions(cfg, opts)
	if err != nil {
		return nil
	}

	sessionsDir, _ := cfg.SessionsDir()
	var entries []hbStatusEntry

	for _, se := range sessions.Sessions {
		if se.LastUserActiveAt == nil {
			continue
		}
		lastActive, parseErr := time.Parse(time.RFC3339, *se.LastUserActiveAt)
		if parseErr != nil {
			continue
		}

		e := hbStatusEntry{
			Key:          se.Key,
			LastActive:   lastActive.Local().Format("15:04"),
			HasHeartbeat: se.HasHeartbeat,
		}

		if now.Sub(lastActive) > hbActivityWindow {
			e.Status = "inactive (>48h)"
			e.NextPulse = "-"
			entries = append(entries, e)
			continue
		}
		if entry, ok := postponed[se.Key]; ok {
			untilT, _ := time.Parse(time.RFC3339, entry.Until)
			createdT, _ := time.Parse(time.RFC3339, entry.CreatedAt)
			if now.Before(untilT) && !lastActive.After(createdT) {
				e.Status = fmt.Sprintf("postponed until %s", untilT.Local().Format("15:04"))
				e.NextPulse = untilT.Local().Format("15:04")
				entries = append(entries, e)
				continue
			}
		}
		if now.Sub(lastActive) < hbQuietMin {
			e.Status = "user active"
			e.NextPulse = lastActive.Add(hbQuietMin).Local().Format("15:04")
			entries = append(entries, e)
			continue
		}
		if se.IsRunning {
			e.Status = "thread running"
			e.NextPulse = "-"
			entries = append(entries, e)
			continue
		}

		// Compute next pulse using real scheduler state.
		sessionDir := hbSessionKeyToDir(sessionsDir, se.Key)
		hbPath := filepath.Join(sessionDir, "heartbeat.md")
		hbMtime := hbFileMtime(hbPath)

		s.mu.Lock()
		prevMtime := s.lastHBMtime[se.Key]
		lp := s.lastPulse[se.Key]
		s.mu.Unlock()

		interval := hbPulseInterval
		if !prevMtime.IsZero() && !hbMtime.IsZero() && hbMtime.After(prevMtime) {
			interval = hbFastPulse
		}

		if lp.IsZero() {
			lp, interval = coldStartAlignment(lastActive, now)
		}

		nextPulse := lp.Add(interval)
		e.NextPulse = nextPulse.Local().Format("15:04:05")
		if now.Before(nextPulse) {
			e.Status = fmt.Sprintf("waiting (%s)", (nextPulse.Sub(now)).Round(time.Second))
		} else {
			e.Status = "due now"
		}
		entries = append(entries, e)
	}
	return entries
}

// coldStartAlignment computes the initial lp and interval on cold start
// (no prior pulse recorded). The pulse timeline is:
//   lastActive+10m, +40m, +70m, ...
//
// If the first pulse (lastActive+10m) hasn't arrived yet, wait for it.
// Otherwise, find the next pulse point >= now and set lp so the fire
// check triggers exactly at that point. Missed pulses are skipped.
func coldStartAlignment(lastActive, now time.Time) (lp time.Time, interval time.Duration) {
	firstPulse := lastActive.Add(hbQuietMin)
	if now.Before(firstPulse) {
		return lastActive, hbQuietMin
	}
	// Find next pulse point >= now using O(1) arithmetic.
	elapsed := now.Sub(firstPulse)
	n := elapsed / hbPulseInterval
	candidate := firstPulse.Add(n * hbPulseInterval)
	if candidate.Before(now) {
		candidate = candidate.Add(hbPulseInterval)
	}
	// candidate is the next pulse point >= now; set lp one interval before
	// so the fire check (now-lp >= interval) passes exactly at candidate.
	return candidate.Add(-hbPulseInterval), hbPulseInterval
}

// hbSessionKeyToDir converts a session key to its directory path.
func hbSessionKeyToDir(sessionsDir, key string) string {
	return session.SessionDir(sessionsDir, key)
}

// hbFileMtime returns the modification time of a file, or zero if it doesn't exist.
func hbFileMtime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// postponeEntry represents a heartbeat postpone with expiry and creation time.
type postponeEntry struct {
	Until     string `json:"until"`
	CreatedAt string `json:"created_at"`
}

// loadPostponeConfig reads heartbeat-postpone.json.
func loadPostponeConfig(path string) map[string]postponeEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]postponeEntry
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// buildHeartbeatMessage constructs a heartbeat system message.
func buildHeartbeatMessage(heartbeatContent, mdModified, nextPulse, hbPath string) string {
	fields := map[string]string{}
	if nextPulse != "" {
		fields["next_pulse"] = nextPulse
	}
	if mdModified != "" {
		fields["heartbeat_modified"] = mdModified
	}

	body := "[heartbeat.md is empty]"
	if c := strings.TrimSpace(heartbeatContent); c != "" {
		body = "## " + hbPath + "\n\n" + c
	} else if hbPath != "" {
		body = "[" + hbPath + " is empty]"
	}

	message := sysmsg.BuildSystemMessage("heartbeat", fields, body)
	message += "\n\nYou must call use_skill(\"heartbeat-wake\") and follow its instructions. use_skill function can not skip."
	return message
}
