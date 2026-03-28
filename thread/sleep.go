package thread

import (
	"fmt"
	"time"

	"github.com/linanwx/nagobot/cron"
)

// SleepThread schedules a persistent delayed wake for this thread's session.
// The thread will be woken with source "sleep_completed" after the duration.
func (t *Thread) SleepThread(duration time.Duration, message string) error {
	addJob := t.cfg().AddJob
	if addJob == nil {
		return fmt.Errorf("sleep not available: job scheduler not configured")
	}

	wakeAt := time.Now().Add(duration).UTC()
	job := cron.Job{
		ID:          "sleep-" + t.id + "-" + RandomHex(4),
		Kind:        cron.JobKindAt,
		AtTime:      &wakeAt,
		Task:        message,
		WakeSession: t.sessionKey,
		DirectWake:  true,
		CreatedAt:   time.Now().UTC(),
	}
	return addJob(job)
}

// IsHeartbeatWake returns true if the current turn was triggered by a heartbeat.
func (t *Thread) IsHeartbeatWake() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastWakeSource == WakeHeartbeat
}

// SetSuppressSink marks the current turn to skip sink delivery.
func (t *Thread) SetSuppressSink() {
	t.mu.Lock()
	t.suppressSink = true
	t.mu.Unlock()
}

// isSinkSuppressed returns whether sink delivery is currently suppressed.
func (t *Thread) isSinkSuppressed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.suppressSink
}

// checkAndResetSinkSuppressed returns the current suppressSink flag and resets it.
func (t *Thread) checkAndResetSinkSuppressed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := t.suppressSink
	t.suppressSink = false
	return v
}

// SetHaltLoop signals the Runner to stop after the current tool calls complete.
func (t *Thread) SetHaltLoop() {
	t.mu.Lock()
	t.haltLoop = true
	t.mu.Unlock()
}

// isHaltLoop returns whether the Runner should halt.
func (t *Thread) isHaltLoop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.haltLoop
}

// resetHaltLoop clears the halt flag at the start of each turn.
func (t *Thread) resetHaltLoop() {
	t.mu.Lock()
	t.haltLoop = false
	t.mu.Unlock()
}
