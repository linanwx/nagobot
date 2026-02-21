package cron

import (
	"fmt"
	"strings"
	"sync"
	"time"

	gocron "github.com/go-co-op/gocron/v2"
)

const (
	JobKindCron = "cron"
	JobKindAt   = "at"
)

type Job struct {
	ID          string     `json:"id" yaml:"id"`
	Kind        string     `json:"kind,omitempty" yaml:"kind,omitempty"`
	Expr        string     `json:"expr,omitempty" yaml:"expr,omitempty"`
	AtTime      *time.Time `json:"at_time,omitempty" yaml:"at_time,omitempty"`
	Task        string     `json:"task" yaml:"task"`
	Agent       string     `json:"agent,omitempty" yaml:"agent,omitempty"`
	WakeSession string     `json:"wake_session,omitempty" yaml:"wake_session,omitempty"`
	Silent      bool       `json:"silent,omitempty" yaml:"silent,omitempty"`
	DirectWake  bool       `json:"direct_wake,omitempty" yaml:"direct_wake,omitempty"`
	CreatedAt   time.Time  `json:"created_at" yaml:"created_at,omitempty"`
}

type ThreadFactory func(job *Job) (string, error)

// Scheduler manages cron and at jobs from two sources:
//   - seedJobs: config-defined defaults, scheduled but not persisted (not in s.jobs)
//   - jobs: store-sourced (cron.jsonl), persisted via saveLocked()
//
// Both share s.cancels for teardown on resetLocked().
type Scheduler struct {
	cron      gocron.Scheduler
	factory   ThreadFactory
	jobs      map[string]Job
	seedJobs  []Job // config-defined seeds, not persisted
	cancels   map[string]func()
	storePath string
	mu        sync.Mutex
}

func NewScheduler(storePath string, factory ThreadFactory, seedJobs []Job) (*Scheduler, error) {
	sch, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("failed to create gocron scheduler: %w", err)
	}
	return &Scheduler{
		cron:      sch,
		factory:   factory,
		jobs:      make(map[string]Job),
		seedJobs:  seedJobs,
		cancels:   make(map[string]func()),
		storePath: strings.TrimSpace(storePath),
	}, nil
}
