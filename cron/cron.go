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

// Job defines one scheduled task.
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

// ThreadFactory executes one job run.
type ThreadFactory func(job *Job) (string, error)

// Scheduler manages cron jobs and persistence.
type Scheduler struct {
	cron      *robfigcron.Cron
	factory   ThreadFactory
	jobs      map[string]*Job
	entryIDs  map[string]robfigcron.EntryID
	timers    map[string]*time.Timer
	storePath string
	mu        sync.Mutex
}

// NewScheduler creates a scheduler with JSON persistence at storePath.
func NewScheduler(storePath string, factory ThreadFactory) *Scheduler {
	return &Scheduler{
		cron:      robfigcron.New(),
		factory:   factory,
		jobs:      make(map[string]*Job),
		entryIDs:  make(map[string]robfigcron.EntryID),
		timers:    make(map[string]*time.Timer),
		storePath: strings.TrimSpace(storePath),
	}
}

// Load loads jobs from store and schedules enabled entries.
func (s *Scheduler) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.resetSchedulesLocked()
	s.jobs = make(map[string]*Job)

	if strings.TrimSpace(s.storePath) == "" {
		return nil
	}

	data, err := os.ReadFile(s.storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var list []Job
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	removedExpired := false
	for _, raw := range list {
		job := normalizeJob(raw)
		if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Task) == "" {
			continue
		}

		switch job.Kind {
		case JobKindCron:
			if strings.TrimSpace(job.Expr) == "" {
				continue
			}
		case JobKindAt:
			if job.AtTime.IsZero() {
				continue
			}
			if job.Enabled && !job.AtTime.After(time.Now().UTC()) {
				removedExpired = true
				continue
			}
		default:
			continue
		}

		j := cloneJob(job)
		s.jobs[j.ID] = j

		if !j.Enabled {
			continue
		}
		if err := s.scheduleLocked(j); err != nil {
			logger.Warn("failed to schedule job from store", "id", j.ID, "kind", j.Kind, "err", err)
		}
	}

	if removedExpired {
		if err := s.saveLocked(); err != nil {
			logger.Warn("failed to save cron store after pruning expired at jobs", "err", err)
		}
	}

	return nil
}

// Save persists current jobs to storePath.
func (s *Scheduler) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// Add validates, schedules, and persists a recurring cron job.
func (s *Scheduler) Add(id, expr, task, agent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	expr = strings.TrimSpace(expr)
	task = strings.TrimSpace(task)
	agent = strings.TrimSpace(agent)

	if id == "" {
		return fmt.Errorf("id is required")
	}
	if expr == "" {
		return fmt.Errorf("expr is required")
	}
	if task == "" {
		return fmt.Errorf("task is required")
	}
	if _, exists := s.jobs[id]; exists {
		return fmt.Errorf("job already exists: %s", id)
	}

	job := &Job{
		ID:        id,
		Kind:      JobKindCron,
		Expr:      expr,
		Task:      task,
		Agent:     agent,
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.scheduleLocked(job); err != nil {
		return err
	}

	s.jobs[job.ID] = cloneJob(*job)
	if err := s.saveLocked(); err != nil {
		s.unscheduleLocked(job.ID)
		delete(s.jobs, job.ID)
		return err
	}
	return nil
}

// AddAt validates, schedules, and persists a one-shot job.
func (s *Scheduler) AddAt(id string, atTime time.Time, task, agent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	task = strings.TrimSpace(task)
	agent = strings.TrimSpace(agent)
	atTime = atTime.UTC()

	if id == "" {
		return fmt.Errorf("id is required")
	}
	if task == "" {
		return fmt.Errorf("task is required")
	}
	if atTime.IsZero() {
		return fmt.Errorf("at_time is required")
	}
	if !atTime.After(time.Now().UTC()) {
		return fmt.Errorf("at_time must be in the future")
	}
	if _, exists := s.jobs[id]; exists {
		return fmt.Errorf("job already exists: %s", id)
	}

	job := &Job{
		ID:        id,
		Kind:      JobKindAt,
		AtTime:    atTime,
		Task:      task,
		Agent:     agent,
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.scheduleLocked(job); err != nil {
		return err
	}

	s.jobs[job.ID] = cloneJob(*job)
	if err := s.saveLocked(); err != nil {
		s.unscheduleLocked(job.ID)
		delete(s.jobs, job.ID)
		return err
	}
	return nil
}

// Remove unschedules and removes a job.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}

	if _, exists := s.jobs[id]; !exists {
		return fmt.Errorf("job not found: %s", id)
	}

	s.unscheduleLocked(id)
	delete(s.jobs, id)
	return s.saveLocked()
}

// List returns all jobs sorted by id.
func (s *Scheduler) List() []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		out = append(out, cloneJob(*job))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// Start starts the internal scheduler.
func (s *Scheduler) Start() {
	s.cron.Start()
}

// Stop stops the scheduler and waits running jobs to return.
func (s *Scheduler) Stop() {
	done := s.cron.Stop().Done()
	<-done

	s.mu.Lock()
	s.resetSchedulesLocked()
	s.mu.Unlock()
}

func (s *Scheduler) saveLocked() error {
	if strings.TrimSpace(s.storePath) == "" {
		return nil
	}

	list := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		list = append(list, *cloneJob(*job))
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.storePath), 0755); err != nil {
		return err
	}

	tmpPath := s.storePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.storePath)
}

func (s *Scheduler) scheduleLocked(job *Job) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	if !job.Enabled {
		return nil
	}

	switch job.Kind {
	case JobKindCron:
		return s.scheduleCronLocked(job)
	case JobKindAt:
		return s.scheduleAtLocked(job)
	default:
		return fmt.Errorf("unsupported job kind: %s", job.Kind)
	}
}

func (s *Scheduler) scheduleCronLocked(job *Job) error {
	if strings.TrimSpace(job.Expr) == "" {
		return fmt.Errorf("job expr is required")
	}

	entryID, err := s.cron.AddFunc(job.Expr, func() {
		if s.factory == nil {
			return
		}
		payload := cloneJob(*job)
		if _, runErr := s.factory(payload); runErr != nil {
			logger.Warn("cron job execution failed", "id", job.ID, "err", runErr)
		}
	})
	if err != nil {
		return err
	}
	s.entryIDs[job.ID] = entryID
	return nil
}

func (s *Scheduler) scheduleAtLocked(job *Job) error {
	if job.AtTime.IsZero() {
		return fmt.Errorf("job at_time is required")
	}
	delay := time.Until(job.AtTime)
	if delay <= 0 {
		return fmt.Errorf("at_time must be in the future")
	}

	payload := cloneJob(*job)
	timer := time.AfterFunc(delay, func() {
		if s.factory != nil {
			if _, err := s.factory(payload); err != nil {
				logger.Warn("at job execution failed", "id", payload.ID, "err", err)
			}
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		if current, ok := s.jobs[payload.ID]; ok && current != nil && current.Kind == JobKindAt {
			delete(s.jobs, payload.ID)
		}
		if timer, ok := s.timers[payload.ID]; ok {
			timer.Stop()
			delete(s.timers, payload.ID)
		}
		if err := s.saveLocked(); err != nil {
			logger.Warn("failed to persist cron store after at job execution", "id", payload.ID, "err", err)
		}
	})
	s.timers[job.ID] = timer
	return nil
}

func (s *Scheduler) unscheduleLocked(id string) {
	if entryID, ok := s.entryIDs[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, id)
	}
	if timer, ok := s.timers[id]; ok {
		timer.Stop()
		delete(s.timers, id)
	}
}

func (s *Scheduler) resetSchedulesLocked() {
	for _, entryID := range s.entryIDs {
		s.cron.Remove(entryID)
	}
	s.entryIDs = make(map[string]robfigcron.EntryID)

	for _, timer := range s.timers {
		timer.Stop()
	}
	s.timers = make(map[string]*time.Timer)
}

func normalizeJob(job Job) Job {
	job.ID = strings.TrimSpace(job.ID)
	job.Kind = strings.ToLower(strings.TrimSpace(job.Kind))
	job.Expr = strings.TrimSpace(job.Expr)
	job.Task = strings.TrimSpace(job.Task)
	job.Agent = strings.TrimSpace(job.Agent)
	job.AtTime = job.AtTime.UTC()

	if job.Kind == "" {
		if !job.AtTime.IsZero() {
			job.Kind = JobKindAt
		} else {
			job.Kind = JobKindCron
		}
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	return job
}

func cloneJob(job Job) *Job {
	copy := job
	return &copy
}
