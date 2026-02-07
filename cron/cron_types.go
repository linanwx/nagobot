package cron

import (
	"strings"
	"sync"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

const (
	JobKindCron = "cron"
	JobKindAt   = "at"
)

type Job struct {
	ID                string    `json:"id" yaml:"id"`
	Kind              string    `json:"kind,omitempty" yaml:"kind,omitempty"`
	Expr              string    `json:"expr,omitempty" yaml:"expr,omitempty"`
	AtTime            time.Time `json:"at_time,omitempty" yaml:"at_time,omitempty"`
	Task              string    `json:"task" yaml:"task"`
	Agent             string    `json:"agent,omitempty" yaml:"agent,omitempty"`
	CreatorSessionKey string    `json:"creator_session_key,omitempty" yaml:"creator_session_key,omitempty"`
	Silent            bool      `json:"silent,omitempty" yaml:"silent,omitempty"`
	Enabled           bool      `json:"enabled" yaml:"enabled"`
	CreatedAt         time.Time `json:"created_at" yaml:"created_at"`
}

type ThreadFactory func(job *Job) (string, error)

type Scheduler struct {
	cron      *robfigcron.Cron
	factory   ThreadFactory
	jobs      map[string]Job
	cancels   map[string]func()
	storePath string
	mu        sync.Mutex
}

func NewScheduler(storePath string, factory ThreadFactory) *Scheduler {
	return &Scheduler{
		cron:      robfigcron.New(),
		factory:   factory,
		jobs:      make(map[string]Job),
		cancels:   make(map[string]func()),
		storePath: strings.TrimSpace(storePath),
	}
}
