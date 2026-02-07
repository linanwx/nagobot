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

// Job defines one scheduled task.
type Job struct {
	ID        string    `json:"id"`
	Expr      string    `json:"expr"`
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
		storePath: strings.TrimSpace(storePath),
	}
}

// Load loads jobs from store and schedules enabled entries.
func (s *Scheduler) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entryID := range s.entryIDs {
		s.cron.Remove(entryID)
	}
	s.jobs = make(map[string]*Job)
	s.entryIDs = make(map[string]robfigcron.EntryID)

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

	for _, raw := range list {
		job := normalizeJob(raw)
		if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Expr) == "" || strings.TrimSpace(job.Task) == "" {
			continue
		}
		j := cloneJob(job)
		if entryID, exists := s.entryIDs[j.ID]; exists {
			s.cron.Remove(entryID)
			delete(s.entryIDs, j.ID)
		}
		s.jobs[j.ID] = j

		if !j.Enabled {
			continue
		}
		if err := s.scheduleLocked(j); err != nil {
			logger.Warn("failed to schedule cron job from store", "id", j.ID, "expr", j.Expr, "err", err)
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

// Add validates, schedules, and persists a new job.
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
		if entryID, ok := s.entryIDs[job.ID]; ok {
			s.cron.Remove(entryID)
			delete(s.entryIDs, job.ID)
		}
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

	if entryID, ok := s.entryIDs[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, id)
	}
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

func normalizeJob(job Job) Job {
	job.ID = strings.TrimSpace(job.ID)
	job.Expr = strings.TrimSpace(job.Expr)
	job.Task = strings.TrimSpace(job.Task)
	job.Agent = strings.TrimSpace(job.Agent)
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	return job
}

func cloneJob(job Job) *Job {
	copy := job
	return &copy
}
