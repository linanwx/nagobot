package cron

import (
	"fmt"
	"strings"
	"time"
)

func validateNew(job Job, existing map[string]Job, now time.Time) error {
	if job.ID == "" {
		return fmt.Errorf("id is required")
	}
	if job.Task == "" {
		return fmt.Errorf("task is required")
	}
	if _, ok := existing[job.ID]; ok {
		return fmt.Errorf("job already exists: %s", job.ID)
	}

	switch job.Kind {
	case JobKindCron:
		if job.Expr == "" {
			return fmt.Errorf("expr is required")
		}
	case JobKindAt:
		if job.AtTime.IsZero() {
			return fmt.Errorf("at_time is required")
		}
		if !job.AtTime.After(now) {
			return fmt.Errorf("at_time must be in the future")
		}
	default:
		return fmt.Errorf("unsupported job kind: %s", job.Kind)
	}
	return nil
}

func validateStored(job Job, now time.Time) (ok bool, expiredAt bool) {
	if job.ID == "" || job.Task == "" {
		return false, false
	}
	switch job.Kind {
	case JobKindCron:
		return job.Expr != "", false
	case JobKindAt:
		if job.AtTime.IsZero() {
			return false, false
		}
		if job.Enabled && !job.AtTime.After(now) {
			return false, true
		}
		return true, false
	}
	return false, false
}

func normalize(job Job) Job {
	job.ID = strings.TrimSpace(job.ID)
	job.Kind = strings.ToLower(strings.TrimSpace(job.Kind))
	job.Expr = strings.TrimSpace(job.Expr)
	job.Task = strings.TrimSpace(job.Task)
	job.Agent = strings.TrimSpace(job.Agent)
	job.CreatorSessionKey = strings.TrimSpace(job.CreatorSessionKey)
	if !job.AtTime.IsZero() {
		job.AtTime = job.AtTime.UTC()
	}

	if job.Kind == "" {
		if job.AtTime.IsZero() {
			job.Kind = JobKindCron
		} else {
			job.Kind = JobKindAt
		}
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	return job
}
