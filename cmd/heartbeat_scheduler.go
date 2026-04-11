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
	hbScanInterval   = 30 * time.Second
	hbQuietMin       = 15 * time.Minute // User must be quiet for at least this long.
	hbPulseInterval  = 45 * time.Minute // Base gap between pulses (grows by hbPulseGrowth each cycle).
	hbPulseGrowth    = 30 * time.Minute // Each subsequent interval grows by this amount.
	hbActivityWindow = 48 * time.Hour   // Only pulse sessions active within this window.
)

// hbSessionState holds persisted per-session heartbeat state.
type hbSessionState struct {
	LastPulse time.Time `json:"last_pulse"`
}

// heartbeatScheduler fires heartbeat pulses into user sessions.
//
// Trigger timeline uses growing intervals aligned to user's last message:
//
//	lastActive+15m, +60m, +115m, +180m, ... (45m base, +10m each cycle)
//
// lastPulse is persisted to disk and only used to prevent duplicate firing
// within the same cycle. It does NOT determine the trigger schedule.
type heartbeatScheduler struct {
	mgr   *thread.Manager
	cfgFn func() *config.Config

	mu       sync.Mutex
	sessions map[string]*hbSessionState // sessionKey → state

	statePath string // path to heartbeat-state.json
}

func newHeartbeatScheduler(mgr *thread.Manager, cfgFn func() *config.Config) *heartbeatScheduler {
	s := &heartbeatScheduler{
		mgr:      mgr,
		cfgFn:    cfgFn,
		sessions: make(map[string]*hbSessionState),
	}
	// Load persisted state.
	if cfg := cfgFn(); cfg != nil {
		if workspace, err := cfg.WorkspacePath(); err == nil {
			s.statePath = filepath.Join(workspace, "system", "heartbeat-state.json")
			s.loadState()
		}
	}
	return s
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

// loadState reads persisted state from disk.
func (s *heartbeatScheduler) loadState() {
	if s.statePath == "" {
		return
	}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var m map[string]*hbSessionState
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	s.mu.Lock()
	s.sessions = m
	s.mu.Unlock()
}

// saveState writes state to disk.
func (s *heartbeatScheduler) saveState() {
	if s.statePath == "" {
		return
	}
	s.mu.Lock()
	data, err := json.Marshal(s.sessions)
	s.mu.Unlock()
	if err != nil {
		return
	}
	dir := filepath.Dir(s.statePath)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(s.statePath, data, 0o644)
}

func (s *heartbeatScheduler) scan(ctx context.Context) {
	now := time.Now()
	logger.Debug("heartbeat scan started")
	cfg := s.cfgFn()
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return
	}

	// Update statePath in case workspace changed.
	s.statePath = filepath.Join(workspace, "system", "heartbeat-state.json")

	postponed := loadPostponeConfig(filepath.Join(workspace, "system", "heartbeat-postpone.json"))

	opts := listSessionsOpts{Days: 2, UserOnly: true}
	sessions, err := collectSessions(cfg, opts)
	if err != nil {
		logger.Warn("heartbeat scan: collectSessions failed", "err", err)
		return
	}
	logger.Debug("heartbeat scan: found sessions", "count", len(sessions.Sessions))

	enrichWithThreads(sessions, s.mgr.ListThreads())

	// Clean up stale entries.
	activeKeys := make(map[string]bool, len(sessions.Sessions))
	for _, se := range sessions.Sessions {
		activeKeys[se.Key] = true
	}
	s.mu.Lock()
	for key := range s.sessions {
		if !activeKeys[key] {
			delete(s.sessions, key)
		}
	}
	s.mu.Unlock()

	for _, se := range sessions.Sessions {
		if ctx.Err() != nil {
			return
		}

		if strings.HasSuffix(se.Key, session.RephraseSessionSuffix) {
			continue
		}
		if strings.Contains(se.Key, session.ForkSessionInfix) {
			continue
		}
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

	s.mu.Lock()
	st := s.sessions[key]
	if st == nil {
		st = &hbSessionState{}
		s.sessions[key] = st
	}
	lastPulse := st.LastPulse
	s.mu.Unlock()

	// Find the latest trigger point on the timeline that is <= now.
	trigger, nextInterval, pulseIndex := latestDueTrigger(lastActive, now)
	if trigger.IsZero() {
		return
	}

	// Only fire if this trigger point hasn't been fired yet.
	if !trigger.After(lastPulse) {
		nextTrigger := trigger.Add(nextInterval)
		logger.Debug("heartbeat skip: already fired this cycle", "key", key,
			"trigger", trigger.Format(time.RFC3339),
			"lastPulse", lastPulse.Format(time.RFC3339),
			"next", nextTrigger.Format(time.RFC3339),
			"wait", nextTrigger.Sub(now).Round(time.Second))
		return
	}

	// Read heartbeat.md mtime for the wake message metadata.
	hbMtime := hbFileMtime(hbPath)

	nextTrigger := trigger.Add(nextInterval)
	nextPulse := nextTrigger.UTC().Format(time.RFC3339)
	mdModified := ""
	if !hbMtime.IsZero() {
		mdModified = hbMtime.UTC().Format(time.RFC3339)
	}
	elapsed := now.Sub(lastActive).Round(time.Second)

	_ = buildHeartbeatMessage(mdModified, nextPulse, pulseIndex, elapsed, lastPulse)

	// TODO: switch to fork wake — create fork session, spawn child thread.
	// s.mgr.Wake(key, &thread.WakeMessage{
	// 	Source:  thread.WakeHeartbeat,
	// 	Message: message,
	// })

	// Update state and persist.
	s.mu.Lock()
	st.LastPulse = now
	s.mu.Unlock()
	s.saveState()

	logger.Info("heartbeat pulse fired", "sessionKey", key, "trigger", trigger.Format(time.RFC3339), "nextPulse", nextPulse)
}

// latestDueTrigger returns the latest trigger point on the timeline
// (lastActive+quietMin, +quietMin+base, +quietMin+base+(base+growth), ...)
// that is <= now, along with the interval to the next trigger point.
// Returns zero time, zero duration, and zero index if no trigger point is due yet.
// pulseIndex is 1-based: the first pulse after quiet threshold is pulse 1.
func latestDueTrigger(lastActive time.Time, now time.Time) (time.Time, time.Duration, int) {
	t := lastActive.Add(hbQuietMin)
	if now.Before(t) {
		return time.Time{}, 0, 0
	}
	idx := 1
	interval := hbPulseInterval
	for {
		next := t.Add(interval)
		if now.Before(next) {
			return t, interval, idx
		}
		t = next
		interval += hbPulseGrowth
		idx++
	}
}

// hbStatusEntry represents one session's heartbeat status.
type hbStatusEntry struct {
	Key          string `json:"key"`
	LastActive   string `json:"last_active"`
	NextPulse    string `json:"next_pulse"`
	Status       string `json:"status"`
	HasHeartbeat bool   `json:"has_heartbeat"`
}

// Status returns the real heartbeat state for all eligible sessions.
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

		// Compute next pulse using persisted state.
		s.mu.Lock()
		var lastPulse time.Time
		if st := s.sessions[se.Key]; st != nil {
			lastPulse = st.LastPulse
		}
		s.mu.Unlock()

		trigger, nextInterval, _ := latestDueTrigger(lastActive, now)
		if trigger.IsZero() {
			e.Status = "user active"
			e.NextPulse = lastActive.Add(hbQuietMin).Local().Format("15:04")
			entries = append(entries, e)
			continue
		}

		if trigger.After(lastPulse) {
			e.Status = "due now"
			e.NextPulse = now.Local().Format("15:04:05")
		} else {
			nextTrigger := trigger.Add(nextInterval)
			e.NextPulse = nextTrigger.Local().Format("15:04:05")
			e.Status = fmt.Sprintf("waiting (%s)", nextTrigger.Sub(now).Round(time.Second))
		}
		entries = append(entries, e)
	}
	return entries
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
// heartbeat.md content is already in the system prompt via heartbeat_prompt_section — no need to duplicate here.
func buildHeartbeatMessage(mdModified, nextPulse string, pulseIndex int, elapsed time.Duration, lastPulse time.Time) string {
	fields := map[string]string{}
	if nextPulse != "" {
		fields["next_pulse"] = nextPulse
	}
	if mdModified != "" {
		fields["heartbeat_modified"] = mdModified
	}
	fields["pulse_index"] = fmt.Sprintf("%d", pulseIndex)
	fields["elapsed_since_user"] = elapsed.String()
	if !lastPulse.IsZero() {
		fields["last_pulse"] = lastPulse.UTC().Format(time.RFC3339)
	}

	message := sysmsg.BuildSystemMessage("heartbeat", fields, "")
	message += "\n\nYou must call use_skill(\"heartbeat-wake\") and follow its instructions. use_skill function can not skip."
	return message
}
