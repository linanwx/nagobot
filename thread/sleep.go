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

// SetSuppressSink marks the current turn to skip sink delivery.
func (t *Thread) SetSuppressSink() {
	t.mu.Lock()
	t.suppressSink = true
	t.mu.Unlock()
}

// checkAndResetSuppressSink returns the current suppressSink flag and resets it.
func (t *Thread) checkAndResetSuppressSink() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := t.suppressSink
	t.suppressSink = false
	return v
}
