package cron

import (
	"sort"
	"time"
)

// SessionRelation says how a job is tied to one session. A job carries no owner
// field; the only links it has to a session are where it RUNS and where it
// REPORTS, so those are the two relations there are.
type SessionRelation string

const (
	// RelationRunsHere: the job's turn executes in this session. Inject mode
	// runs in WakeSession; independent mode runs in its own cron:<ID>.
	RelationRunsHere SessionRelation = "runs_here"
	// RelationReportsHere: an independent-mode job runs in cron:<ID> and names
	// this session as the place its results go.
	RelationReportsHere SessionRelation = "reports_here"
)

// IndependentSessionKey is the session an independent-mode job runs in.
func IndependentSessionKey(jobID string) string { return "cron:" + jobID }

// RelationTo reports how job relates to sessionKey, or "" when it does not.
func RelationTo(job Job, sessionKey string) SessionRelation {
	if sessionKey == "" {
		return ""
	}
	if job.DirectWake {
		if job.WakeSession == sessionKey {
			return RelationRunsHere
		}
		return ""
	}
	if IndependentSessionKey(job.ID) == sessionKey {
		return RelationRunsHere
	}
	if job.WakeSession == sessionKey {
		return RelationReportsHere
	}
	return ""
}

// Jobs returns every job the scheduler currently holds: persisted jobs plus the
// config seeds not overridden by a stored job of the same ID, sorted by ID.
// This is the live set, not cron.jsonl — seeds never reach the store, and the
// store may still list an at-job that has already fired.
func (s *Scheduler) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs)+len(s.seedJobs))
	for _, j := range s.jobs {
		out = append(out, j)
	}
	for _, raw := range s.seedJobs {
		j := Normalize(raw)
		if _, overridden := s.jobs[j.ID]; overridden {
			continue
		}
		if ok, _ := ValidateStored(j, time.Now()); !ok {
			continue // Load skipped it too, so it is not scheduled
		}
		// Normalize stamps a zero CreatedAt with "now"; a seed was never
		// created, and reporting it as created this instant would be a lie.
		j.CreatedAt = raw.CreatedAt
		out = append(out, j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}
