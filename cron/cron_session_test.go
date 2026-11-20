package cron

import (
	"testing"
)

func TestRelationTo(t *testing.T) {
	inject := Job{ID: "nudge", Kind: JobKindCron, Expr: "0 8 * * *", Task: "t", WakeSession: "telegram:1", DirectWake: true}
	indep := Job{ID: "brief", Kind: JobKindCron, Expr: "0 9 * * *", Task: "t", Agent: "general", WakeSession: "telegram:1"}
	silent := Job{ID: "tidyup", Kind: JobKindCron, Expr: "0 4 * * *", Task: "t", Agent: "tidyup"}

	cases := []struct {
		job  Job
		key  string
		want SessionRelation
	}{
		{inject, "telegram:1", RelationRunsHere},
		// An inject job never runs in cron:<id>, so that session has nothing to show.
		{inject, "cron:nudge", ""},
		{indep, "cron:brief", RelationRunsHere},
		{indep, "telegram:1", RelationReportsHere},
		{silent, "cron:tidyup", RelationRunsHere},
		{silent, "telegram:1", ""},
		{indep, "", ""},
	}
	for _, c := range cases {
		if got := RelationTo(c.job, c.key); got != c.want {
			t.Errorf("RelationTo(%s, %q) = %q, want %q", c.job.ID, c.key, got, c.want)
		}
	}
}

func TestSchedulerJobsIncludesSeedsUnlessOverridden(t *testing.T) {
	dir := t.TempDir()
	stored := Job{ID: "a", Kind: JobKindCron, Expr: "0 1 * * *", Task: "stored a"}
	if err := WriteJobs(dir+"/cron.jsonl", []Job{stored}); err != nil {
		t.Fatal(err)
	}
	seeds := []Job{
		{ID: "a", Expr: "0 2 * * *", Task: "seed a"},
		{ID: "b", Expr: "0 3 * * *", Task: "seed b"},
		{ID: "bad", Task: ""}, // invalid: Load never schedules it
	}
	s, err := NewScheduler(dir+"/cron.jsonl", nil, seeds)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}

	jobs := s.Jobs()
	if len(jobs) != 2 || jobs[0].ID != "a" || jobs[1].ID != "b" {
		t.Fatalf("Jobs() = %+v, want [a b]", jobs)
	}
	if jobs[0].Task != "stored a" {
		t.Errorf("a.Task = %q, want the stored job to override the seed", jobs[0].Task)
	}
	if !jobs[1].CreatedAt.IsZero() {
		t.Errorf("seed CreatedAt = %v, want zero (a seed was never created)", jobs[1].CreatedAt)
	}
}
