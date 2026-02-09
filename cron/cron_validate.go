package cron

import (
	"strings"
	"time"
)

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
		if !job.AtTime.After(now) {
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
