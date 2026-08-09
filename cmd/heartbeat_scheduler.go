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

	// hbReflectMinPulse is the EARLIEST pulse that may run session-reflect. On
	// the timeline above, pulse 4 lands 4h00m after the user's last message —
	// late enough that the conversation is really over, not just a lunch break,
	// and late enough that the provider's prompt cache has expired anyway.
	//
	// A floor rather than an equality, so a pulse lost to a higher-priority task
	// is retried on the next one. See heartbeatTasks for the measurement that
	// forced the change.
	hbReflectMinPulse = 4

	// hbDreamMinPulse is the earliest pulse that may run a dream. Pulse 2 is one
	// hour of quiet — enough that a message sent at 01:50 does not get its
	// context rewritten underneath it at 02:05.
	hbDreamMinPulse = 2
)

// hbSessionState holds persisted per-session heartbeat state.
//
// Fired is the ONLY dedup authority: every task's "have I run recently"
// predicate reads it, and it is written and persisted at fire time, so a restart
// cannot re-run work that already happened. heartbeat_log.jsonl is append-only
// observability and is never read back.
type hbSessionState struct {
	LastPulse time.Time     `json:"last_pulse"`
	Fired     []firedRecord `json:"fired,omitempty"`
}

// heartbeatScheduler fires heartbeat pulses into user sessions.
//
// The trigger timeline is pulseSchedule, anchored on the user's last message:
//
//	lastActive+15m, +60m, +135m, +240m, +375m, ... (45m base gap, +30m each cycle)
//
// What a due pulse actually DOES is heartbeatTasks — a priority-ordered table of
// predicates, of which at most one runs per pulse. Most pulses run nothing.
//
// lastPulse is persisted to disk and only used to keep one pulse from being
// evaluated twice. It does NOT determine the trigger schedule.
type heartbeatScheduler struct {
	mgr   *thread.Manager
	cfgFn func() *config.Config

	mu       sync.Mutex
	sessions map[string]*hbSessionState // sessionKey → state

	statePath string // path to heartbeat-state.json

	taskLogPath string // path to heartbeat_log.jsonl (append-only, never read back)

	summaryPath string // path to sessions_summary.json (read only on dream pulses)
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
			s.taskLogPath = filepath.Join(workspace, "system", "heartbeat_log.jsonl")
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

// taskLogEntry is one append-only record in heartbeat_log.jsonl. It is written
// for observability only — "which task ran, for whom, on which pulse" is the
// question the deployment gets asked months later, and it is not answerable from
// heartbeat-state.json, which only ever holds the last 48 hours.
//
// It is never read back. Dedup reads hbSessionState.Fired, which is persisted at
// the same moment; two readers of two files would be two chances to disagree.
type taskLogEntry struct {
	SessionKey string    `json:"session_key"`
	Task       string    `json:"task"`
	At         time.Time `json:"at"`
	Pulse      int       `json:"pulse"`
}

// recordFired marks a task as run for this session and appends it to the task
// log. Called at fire time, not at completion, so the dedup window holds even
// when the LLM turn behind it fails — the next chance is the next pulse the
// predicate admits, never an immediate retry loop.
func (s *heartbeatScheduler) recordFired(key, task string, now time.Time, pulse int, epoch time.Time) {
	rec := firedRecord{Task: task, At: now.UTC(), Pulse: pulse, Epoch: epoch.UTC()}

	s.mu.Lock()
	st := s.sessions[key]
	if st == nil {
		st = &hbSessionState{}
		s.sessions[key] = st
	}
	st.Fired = append(pruneFired(st.Fired, now), rec)
	s.mu.Unlock()

	if s.taskLogPath == "" {
		return
	}
	data, err := json.Marshal(taskLogEntry{SessionKey: key, Task: task, At: rec.At, Pulse: pulse})
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(s.taskLogPath), 0o755)
	f, err := os.OpenFile(s.taskLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Warn("heartbeat task log append failed", "key", key, "task", task, "err", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		logger.Warn("heartbeat task log write failed", "key", key, "task", task, "err", err)
	}
}

// sessionLoc resolves the session's configured timezone, falling back to the
// system zone when unset or invalid.
func (s *heartbeatScheduler) sessionLoc(key string) *time.Location {
	loc, err := time.LoadLocation(s.cfgFn().SessionTimezone(key))
	if err != nil {
		return time.Local
	}
	return loc
}

// pulseStateFor assembles what the task predicates get to see. Kept in one place
// so the scan path and `heartbeat status` cannot disagree about what would fire.
func (s *heartbeatScheduler) pulseStateFor(key string, p scheduledPulse, now, lastActive time.Time) pulseState {
	s.mu.Lock()
	var fired []firedRecord
	if st := s.sessions[key]; st != nil {
		fired = append(fired, st.Fired...)
	}
	s.mu.Unlock()

	return pulseState{
		SessionKey: key,
		Index:      p.Index,
		Now:        now,
		Trigger:    p.At,
		LastActive: lastActive,
		Elapsed:    now.Sub(lastActive).Round(time.Second),
		Fired:      fired,
		Loc:        s.sessionLoc(key),
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

	// Find the latest pulse on this quiet period's roster that is <= now.
	pulse, nextInterval, ok := newPulseSchedule(lastActive).latest(now)
	if !ok {
		return
	}

	// Only evaluate a pulse once. lastPulse is purely this dedup guard — it
	// never determines when the next pulse is due, which is a pure function of
	// lastActive.
	if !pulse.At.After(lastPulse) {
		nextTrigger := pulse.At.Add(nextInterval)
		logger.Debug("heartbeat skip: already evaluated this pulse", "key", key,
			"trigger", pulse.At.Format(time.RFC3339),
			"lastPulse", lastPulse.Format(time.RFC3339),
			"next", nextTrigger.Format(time.RFC3339),
			"wait", nextTrigger.Sub(now).Round(time.Second))
		return
	}

	state := s.pulseStateFor(key, pulse, now, lastActive)
	task := selectHeartbeatTask(state)

	if task != "" {
		nextPulse := pulse.At.Add(nextInterval).UTC().Format(time.RFC3339)
		mdModified := ""
		if hbMtime := hbFileMtime(hbPath); !hbMtime.IsZero() {
			mdModified = hbMtime.UTC().Format(time.RFC3339)
		}
		// Read only on a dream pulse — at most once a night per session, so the
		// file read never lands on an ordinary pulse (most of which do nothing).
		summary := ""
		if task == hbTaskDream {
			summary = s.sessionSummary(key)
		}
		message := buildHeartbeatMessage(mdModified, nextPulse, pulse.Index, state.Elapsed, lastPulse, task, summary)

		s.mgr.Wake(key, &thread.WakeMessage{
			Source:  thread.WakeHeartbeat,
			Message: message,
			Sinks: thread.NewSinks(thread.SessionSink{
				Label: "heartbeat pulse — nothing produced this turn reaches the user, by design",
				Send:  func(_ context.Context, _ string) error { return nil },
			}),
		})
		s.recordFired(key, task, now, pulse.Index, lastActive)
		logger.Info("heartbeat task fired", "sessionKey", key, "task", task,
			"pulse", pulse.Index, "trigger", pulse.At.Format(time.RFC3339), "nextPulse", nextPulse)
	} else {
		// The common case by a wide margin, and it costs no LLM call. Logged at
		// Debug precisely because it is not an event.
		logger.Debug("heartbeat pulse: no task eligible", "key", key, "pulse", pulse.Index)
	}

	// Update state and persist. Unconditional: the pulse was evaluated whether
	// or not anything came of it, and re-evaluating it every 30s until the next
	// one would re-ask a question already answered.
	s.mu.Lock()
	st.LastPulse = now
	s.mu.Unlock()
	s.saveState()
}

// hbStatusEntry represents one session's heartbeat status.
type hbStatusEntry struct {
	Key          string `json:"key"`
	LastActive   string `json:"last_active"`
	NextPulse    string `json:"next_pulse"`
	Status       string `json:"status"`
	HasHeartbeat bool   `json:"has_heartbeat"`
	// Roster is the next few pulses and the task each would run if it fired
	// right then, e.g. "p4 04:12 reflect". Predicted with the same
	// selectHeartbeatTask the scan path uses, so an empty roster is a real
	// answer — this session has nothing scheduled — rather than a missing view.
	Roster []string `json:"roster,omitempty"`
	// Fired is what already ran during the current quiet period.
	Fired []string `json:"fired,omitempty"`
}

// hbStatusRosterLen is how far ahead `heartbeat status` looks. Four pulses is
// roughly the next 12 hours once the gaps have grown, which covers "will this
// session dream tonight" — the question the roster exists to answer.
const hbStatusRosterLen = 4

// rosterFor predicts the upcoming pulses and what each would do.
//
// Predictions are threaded FORWARD: a task predicted for one pulse is added to
// the fired set the next pulse is evaluated against, exactly as the scan path
// would have recorded it. Evaluating each pulse independently instead would show
// an owed reflect on every remaining row, reading as "reflect runs four times"
// when it runs once and the rest go quiet.
//
// The prediction is still only a prediction: eligibility is evaluated at each
// pulse's scheduled moment, so a dream shown for 03:15 fires only if the daemon
// is awake and the session still quiet then.
func (s *heartbeatScheduler) rosterFor(key string, now, lastActive time.Time) []string {
	sched := newPulseSchedule(lastActive)
	var predicted []firedRecord
	var out []string
	for _, p := range sched.upcoming(now, hbStatusRosterLen) {
		state := s.pulseStateFor(key, p, p.At, lastActive)
		state.Fired = append(state.Fired, predicted...)

		task := selectHeartbeatTask(state)
		label := task
		if task == "" {
			label = "-"
		} else {
			predicted = append(predicted, firedRecord{Task: task, At: p.At, Pulse: p.Index, Epoch: lastActive})
		}
		out = append(out, fmt.Sprintf("p%d %s %s", p.Index, p.At.Local().Format("15:04"), label))
	}
	return out
}

// firedFor lists this quiet period's completed tasks, newest last.
func (s *heartbeatScheduler) firedFor(key string, lastActive time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.sessions[key]
	if st == nil {
		return nil
	}
	var out []string
	for _, f := range st.Fired {
		if f.Epoch.Equal(lastActive.UTC()) {
			out = append(out, fmt.Sprintf("%s p%d %s", f.Task, f.Pulse, f.At.Local().Format("15:04")))
		}
	}
	return out
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
		e.Roster = s.rosterFor(se.Key, now, lastActive)
		e.Fired = s.firedFor(se.Key, lastActive)
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
// task is the name the scheduler already selected, and it is what the
// heartbeat-wake skill routes on. The routing decision is made HERE, in Go,
// rather than restated as index arithmetic in markdown — the pulse number used
// to live in both places, and changing one without the other silently disabled
// the task.
func buildHeartbeatMessage(mdModified, nextPulse string, pulseIndex int, elapsed time.Duration, lastPulse time.Time, task string, sessionSummary string) string {
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
	if task != "" {
		fields["task"] = task
	}
	if task == hbTaskDream {
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
