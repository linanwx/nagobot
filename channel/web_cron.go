package channel

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/linanwx/nagobot/config"
	cronpkg "github.com/linanwx/nagobot/cron"
	robfigcron "github.com/robfig/cron/v3"
)

// --- GET /api/sessions/{key...}/cron ---
//
// The cron jobs tied to one session. Jobs carry no owner field, so "tied to" is
// derived from the two links a job does have — where it runs and where it
// reports (cronpkg.RelationTo). Read-only: jobs are created and removed by the
// model through `nagobot cron`, and this panel exists so a person can see what
// a session has scheduled without reading the whole deployment's cron list.

type sessionCronJob struct {
	ID       string                  `json:"id"`
	Relation cronpkg.SessionRelation `json:"relation"`
	Kind     string                  `json:"kind"`
	// Schedule is the cron expression, or the RFC3339 instant of a one-time job.
	Schedule string `json:"schedule"`
	// NextRun is the next fire time; omitted when the expression does not parse.
	NextRun     *time.Time `json:"next_run,omitempty"`
	Mode        string     `json:"mode"` // "inject" | "independent"
	Agent       string     `json:"agent,omitempty"`
	WakeSession string     `json:"wake_session,omitempty"`
	// RunSession is where the job's turn executes, so the panel can link to it.
	RunSession string     `json:"run_session"`
	Task       string     `json:"task"`
	Builtin    bool       `json:"builtin,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
}

type sessionCronResponse struct {
	Key string `json:"key"`
	// Timezone the daemon evaluates cron expressions in — "0 9 * * *" means
	// 09:00 THERE, which need not be where the browser is.
	Timezone string           `json:"timezone"`
	Jobs     []sessionCronJob `json:"jobs"`
}

// SetCronJobsFn sets the lister behind GET /api/sessions/{key}/cron.
func (w *WebChannel) SetCronJobsFn(fn func() []cronpkg.Job) {
	w.cronJobsFn = fn
}

func (w *WebChannel) handleSessionCron(rw http.ResponseWriter, key string) {
	if w.cronJobsFn == nil {
		http.Error(rw, "cron scheduler is not available", http.StatusServiceUnavailable)
		return
	}
	now := time.Now()
	resp := sessionCronResponse{
		Key:      key,
		Timezone: zoneLabel(now),
		Jobs:     sessionCronJobs(w.cronJobsFn(), key, now),
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(resp)
}

// zoneLabel names the daemon's zone with its current offset. Without TZ set,
// Go's location name is the literal "Local", which tells a reader nothing.
func zoneLabel(now time.Time) string {
	name := now.Location().String()
	abbr := now.Format("MST")
	offset := "UTC" + now.Format("-07:00")
	if name == "Local" || name == abbr {
		return abbr + " (" + offset + ")"
	}
	return name + " (" + abbr + ", " + offset + ")"
}

// sessionCronJobs filters jobs to the ones tied to key, ordered by next fire
// time (soonest first; unparseable schedules last, then by ID).
func sessionCronJobs(jobs []cronpkg.Job, key string, now time.Time) []sessionCronJob {
	out := []sessionCronJob{}
	for _, job := range jobs {
		rel := cronpkg.RelationTo(job, key)
		if rel == "" {
			continue
		}
		e := sessionCronJob{
			ID:          job.ID,
			Relation:    rel,
			Kind:        job.Kind,
			Mode:        "independent",
			Agent:       job.Agent,
			WakeSession: job.WakeSession,
			RunSession:  cronpkg.IndependentSessionKey(job.ID),
			Task:        job.Task,
			Builtin:     config.IsBuiltinCronJob(job.ID),
		}
		if job.DirectWake {
			e.Mode = "inject"
			e.RunSession = job.WakeSession
		}
		if !job.CreatedAt.IsZero() {
			t := job.CreatedAt
			e.CreatedAt = &t
		}
		switch job.Kind {
		case cronpkg.JobKindAt:
			if job.AtTime != nil {
				t := *job.AtTime
				e.Schedule = t.Format(time.RFC3339)
				e.NextRun = &t
			}
		default:
			e.Schedule = job.Expr
			// gocron evaluates CronJob(expr, false) in time.Local; so does this.
			if sched, err := robfigcron.ParseStandard(job.Expr); err == nil {
				t := sched.Next(now)
				e.NextRun = &t
			}
		}
		out = append(out, e)
	}
	sortSessionCronJobs(out)
	return out
}

func sortSessionCronJobs(jobs []sessionCronJob) {
	sort.SliceStable(jobs, func(i, j int) bool {
		a, b := jobs[i], jobs[j]
		if (a.NextRun == nil) != (b.NextRun == nil) {
			return a.NextRun != nil
		}
		if a.NextRun != nil && !a.NextRun.Equal(*b.NextRun) {
			return a.NextRun.Before(*b.NextRun)
		}
		return a.ID < b.ID
	})
}
