package cron

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
	robfigcron "github.com/robfig/cron/v3"
)

const (
	JobKindCron = "cron"
	JobKindAt   = "at"
)

type Job struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind,omitempty"`
	Expr      string    `json:"expr,omitempty"`
	AtTime    time.Time `json:"at_time,omitempty"`
	Task      string    `json:"task"`
	Agent     string    `json:"agent,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
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

func (s *Scheduler) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.resetLocked()
	list, err := s.readStore()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	dirty := false
	for _, raw := range list {
		job := normalize(raw)
		ok, expired := validateStored(job, now)
		if !ok {
			if expired {
				dirty = true
			}
			continue
		}

		s.jobs[job.ID] = job
		cancel, err := s.scheduleLocked(job)
		if err != nil {
			logger.Warn("failed to schedule job from store", "id", job.ID, "kind", job.Kind, "err", err)
			continue
		}
		if cancel != nil {
			s.cancels[job.ID] = cancel
		}
	}

	if dirty {
		if err := s.saveLocked(); err != nil {
			logger.Warn("failed to save cron store after pruning expired at jobs", "err", err)
		}
	}
	return nil
}

func (s *Scheduler) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Scheduler) Add(id, expr, task, agent string) error {
	return s.add(Job{
		ID:        strings.TrimSpace(id),
		Kind:      JobKindCron,
		Expr:      strings.TrimSpace(expr),
		Task:      strings.TrimSpace(task),
		Agent:     strings.TrimSpace(agent),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Scheduler) AddAt(id string, atTime time.Time, task, agent string) error {
	return s.add(Job{
		ID:        strings.TrimSpace(id),
		Kind:      JobKindAt,
		AtTime:    atTime.UTC(),
		Task:      strings.TrimSpace(task),
		Agent:     strings.TrimSpace(agent),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Scheduler) add(job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateNew(job, s.jobs, time.Now().UTC()); err != nil {
		return err
	}

	cancel, err := s.scheduleLocked(job)
	if err != nil {
		return err
	}

	s.jobs[job.ID] = job
	if cancel != nil {
		s.cancels[job.ID] = cancel
	}
	if err := s.saveLocked(); err != nil {
		s.unscheduleLocked(job.ID)
		delete(s.jobs, job.ID)
		return err
	}
	return nil
}

func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if _, ok := s.jobs[id]; !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	s.unscheduleLocked(id)
	delete(s.jobs, id)
	return s.saveLocked()
}

func (s *Scheduler) List() []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		j := job
		out = append(out, &j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Scheduler) Start() { s.cron.Start() }

func (s *Scheduler) Stop() {
	done := s.cron.Stop().Done()
	<-done

	s.mu.Lock()
	s.resetLocked()
	s.mu.Unlock()
}

func (s *Scheduler) scheduleLocked(job Job) (func(), error) {
	if !job.Enabled {
		return nil, nil
	}

	switch job.Kind {
	case JobKindCron:
		entryID, err := s.cron.AddFunc(job.Expr, func() {
			if s.factory == nil {
				return
			}
			j := job
			if _, runErr := s.factory(&j); runErr != nil {
				logger.Warn("cron job execution failed", "id", job.ID, "err", runErr)
			}
		})
		if err != nil {
			return nil, err
		}
		return func() { s.cron.Remove(entryID) }, nil

	case JobKindAt:
		delay := time.Until(job.AtTime)
		if delay <= 0 {
			return nil, fmt.Errorf("at_time must be in the future")
		}

		timer := time.AfterFunc(delay, func() {
			if s.factory != nil {
				j := job
				if _, err := s.factory(&j); err != nil {
					logger.Warn("at job execution failed", "id", job.ID, "err", err)
				}
			}

			s.mu.Lock()
			delete(s.jobs, job.ID)
			delete(s.cancels, job.ID)
			if err := s.saveLocked(); err != nil {
				logger.Warn("failed to persist cron store after at job execution", "id", job.ID, "err", err)
			}
			s.mu.Unlock()
		})
		return func() { timer.Stop() }, nil
	}

	return nil, fmt.Errorf("unsupported job kind: %s", job.Kind)
}

func (s *Scheduler) unscheduleLocked(id string) {
	if cancel, ok := s.cancels[id]; ok {
		cancel()
		delete(s.cancels, id)
	}
}

func (s *Scheduler) resetLocked() {
	for _, cancel := range s.cancels {
		cancel()
	}
	s.jobs = make(map[string]Job)
	s.cancels = make(map[string]func())
}

func (s *Scheduler) readStore() ([]Job, error) {
	if s.storePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var list []Job
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Scheduler) saveLocked() error {
	if s.storePath == "" {
		return nil
	}

	list := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		list = append(list, job)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.storePath), 0755); err != nil {
		return err
	}
	tmp := s.storePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.storePath)
}

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
