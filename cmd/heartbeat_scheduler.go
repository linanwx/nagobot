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
	hbDreamDedup     = 4 * time.Hour    // After a dream fires, suppress dreams for this session for this long.

	// hbReflectPulse is the pulse index that triggers session-reflect. On the
	// timeline above, pulse 4 lands 4h00m after the user's last message —
	// late enough that the conversation is really over, not just a lunch break.
	// The heartbeat-wake skill routes on this same number: keep the two in sync.
	hbReflectPulse = 4
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

	dreamLogPath string               // path to dream_log.jsonl
	lastDream    map[string]time.Time // sessionKey → last dream time (dedup, guarded by mu)

	summaryPath string // path to sessions_summary.json (read only on dream pulses)
}

func newHeartbeatScheduler(mgr *thread.Manager, cfgFn func() *config.Config) *heartbeatScheduler {
	s := &heartbeatScheduler{
		mgr:       mgr,
		cfgFn:     cfgFn,
		sessions:  make(map[string]*hbSessionState),
		lastDream: make(map[string]time.Time),
	}
	// Load persisted state.
	if cfg := cfgFn(); cfg != nil {
		if workspace, err := cfg.WorkspacePath(); err == nil {
			s.statePath = filepath.Join(workspace, "system", "heartbeat-state.json")
			s.loadState()
			s.dreamLogPath = filepath.Join(workspace, "system", "dream_log.jsonl")
			s.loadDreamLog()
			s.summaryPath = filepath.Join(workspace, "system", "sessions_summary.json")
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

// dreamLogEntry is one append-only record in dream_log.jsonl.
type dreamLogEntry struct {
	SessionKey string    `json:"session_key"`
	DreamedAt  time.Time `json:"dreamed_at"`
}

// loadDreamLog reads the append-only dream log and rebuilds the per-session
// last-dream map, keeping the most recent timestamp per session. This survives
// restarts so a nighttime restart does not re-trigger dreams already done.
func (s *heartbeatScheduler) loadDreamLog() {
	if s.dreamLogPath == "" {
		return
	}
	data, err := os.ReadFile(s.dreamLogPath)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e dreamLogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.SessionKey == "" {
			continue
		}
		if prev, ok := s.lastDream[e.SessionKey]; !ok || e.DreamedAt.After(prev) {
			s.lastDream[e.SessionKey] = e.DreamedAt
		}
	}
}

// recordDream appends a dream event to dream_log.jsonl and updates the in-memory
// dedup map. Called at fire time so the dedup window holds even if the LLM's
// dream turn fails — the next dream chance is the following night.
func (s *heartbeatScheduler) recordDream(key string, now time.Time) {
	s.mu.Lock()
	s.lastDream[key] = now
	s.mu.Unlock()
	if s.dreamLogPath == "" {
		return
	}
	data, err := json.Marshal(dreamLogEntry{SessionKey: key, DreamedAt: now.UTC()})
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(s.dreamLogPath), 0o755)
	f, err := os.OpenFile(s.dreamLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Warn("dream log append failed", "key", key, "err", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		logger.Warn("dream log write failed", "key", key, "err", err)
	}
}

// nightHour reports whether now falls in the 02:00–06:00 window of the session's
// configured timezone, falling back to the system timezone when unset/invalid.
func (s *heartbeatScheduler) nightHour(key string, now time.Time) bool {
	tz := s.cfgFn().SessionTimezone(key)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}
	h := now.In(loc).Hour()
	return h >= 2 && h < 6
}

// shouldDream decides whether this pulse should also trigger a dream.
// Conditions: pulse_index > 2 (user quiet > ~135 min), session-local night
// (02:00–06:00), and no dream for this session within the dedup window.
func (s *heartbeatScheduler) shouldDream(key string, now time.Time, pulseIndex int) bool {
	if pulseIndex <= 2 {
		return false
	}
	if !s.nightHour(key, now) {
		return false
	}
	s.mu.Lock()
	last := s.lastDream[key]
	s.mu.Unlock()
	if !last.IsZero() && now.Sub(last) < hbDreamDedup {
		return false
	}
	return true
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
	s.summaryPath = filepath.Join(workspace, "system", "sessions_summary.json")

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

		if session.IsInternalSiblingSession(se.Key) {
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

	dream := s.shouldDream(key, now, pulseIndex)
	// Read only on a dream pulse — once a night per session at most, so the
	// file read never lands on an ordinary pulse (most of which wake nothing).
	summary := ""
	if dream {
		summary = s.sessionSummary(key)
	}
	message := buildHeartbeatMessage(mdModified, nextPulse, pulseIndex, elapsed, lastPulse, dream, summary)

	// Wake for pulse indices that have registered handlers, or when a dream is due.
	// hbReflectPulse (4h00m) → session-reflect; pulse_index > 2 at night → dream.
	// Every other pulse wakes nothing and costs no LLM call.
	if pulseIndex == hbReflectPulse || dream {
		s.mgr.Wake(key, &thread.WakeMessage{
			Source:  thread.WakeHeartbeat,
			Message: message,
			Sinks: thread.NewSinks(thread.SessionSink{
				Label: "heartbeat pulse — nothing produced this turn reaches the user, by design",
				Send:  func(_ context.Context, _ string) error { return nil },
			}),
		})
		if dream {
			s.recordDream(key, now)
		}
	}

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

// sessionSummary returns the session's current one-line summary from
// sessions_summary.json, or "" when the session has no entry (or the file is
// unreadable — an absent summary and an unreadable store are the same thing to
// the dream, which is told to write one either way).
func (s *heartbeatScheduler) sessionSummary(key string) string {
	if s.summaryPath == "" {
		return ""
	}
	return strings.Join(strings.Fields(loadSummariesFile(s.summaryPath)[key].Summary), " ")
}

// noSessionSummary is what the wake carries when the session has no summary on
// record. Stated rather than omitted: an absent field reads as "not applicable"
// and the dream would skip step 4, which is exactly the case where it must NOT
// skip — a session with no summary has never had one written.
const noSessionSummary = "(none on record — this session has never had a summary; write one)"

// buildHeartbeatMessage constructs a heartbeat system message.
// heartbeat.md content is already in the system prompt via heartbeat_prompt_section — no need to duplicate here.
//
// sessionSummary is carried ONLY on a dream pulse, and only because the dream
// decides whether to rewrite it. It duplicates a row the system prompt already
// has (the cross-session awareness section), and that duplication is the point:
// that section lists every session, so the dream had to find its own row among
// them and judge staleness from a line it might not locate. Here the summary
// under judgement is the wake's own field.
func buildHeartbeatMessage(mdModified, nextPulse string, pulseIndex int, elapsed time.Duration, lastPulse time.Time, shouldDream bool, sessionSummary string) string {
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
	if shouldDream {
		fields["should_dream"] = "true"
		if sessionSummary != "" {
			fields["session_summary"] = sessionSummary
		} else {
			fields["session_summary"] = noSessionSummary
		}
	}

	message := sysmsg.BuildSystemMessage("heartbeat", fields, "")
	message += "\n\nYou must call use_skill(\"heartbeat-wake\") and follow its instructions. use_skill function can not skip."
	return message
}
