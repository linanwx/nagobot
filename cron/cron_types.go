package cron

import (
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
	ID                string    `json:"id"`
	Kind              string    `json:"kind,omitempty"`
	Expr              string    `json:"expr,omitempty"`
	AtTime            time.Time `json:"at_time,omitempty"`
	Task              string    `json:"task"`
	Agent             string    `json:"agent,omitempty"`
	CreatorSessionKey string    `json:"creator_session_key,omitempty"`
	Silent            bool      `json:"silent,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type ThreadFactory func(job *Job) (string, error)

type Scheduler struct {
	cron      gocron.Scheduler
	factory   ThreadFactory
	jobs      map[string]Job
	cancels   map[string]func()
	storePath string
	mu        sync.Mutex
}

func NewScheduler(storePath string, factory ThreadFactory) *Scheduler {
	sch, _ := gocron.NewScheduler()
	return &Scheduler{
		cron:      sch,
		factory:   factory,
		jobs:      make(map[string]Job),
		cancels:   make(map[string]func()),
		storePath: strings.TrimSpace(storePath),
	}
}
