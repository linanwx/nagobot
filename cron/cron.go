// Package cron provides scheduled task execution and health monitoring.
package cron

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// Scheduler manages scheduled jobs.
type Scheduler struct {
	mu      sync.RWMutex
	jobs    map[string]*Job
	running bool
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewScheduler creates a new job scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		jobs: make(map[string]*Job),
		done: make(chan struct{}),
	}
}

// Add adds a job to the scheduler.
func (s *Scheduler) Add(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job already exists: %s", job.ID)
	}

	// Calculate next run time
	job.NextRun = job.Schedule.Next(time.Now())
	s.jobs[job.ID] = job

	logger.Info("job added", "id", job.ID, "nextRun", job.NextRun.Format("15:04:05"))
	return nil
}

// Remove removes a job from the scheduler.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[id]; !exists {
		return fmt.Errorf("job not found: %s", id)
	}

	delete(s.jobs, id)
	logger.Info("job removed", "id", id)
	return nil
}

// Get returns a job by ID.
func (s *Scheduler) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

// List returns all jobs sorted by next run time.
func (s *Scheduler) List() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}

	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].NextRun.Before(jobs[j].NextRun)
	})

	return jobs
}

// Start begins the scheduler loop.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}
	s.running = true
	s.done = make(chan struct{})
	s.mu.Unlock()

	logger.Info("scheduler started")

	s.wg.Add(1)
	go s.run(ctx)

	return nil
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	close(s.done)
	s.mu.Unlock()

	s.wg.Wait()
	logger.Info("scheduler stopped")
	return nil
}

// run is the main scheduler loop.
func (s *Scheduler) run(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case now := <-ticker.C:
			s.tick(ctx, now)
		}
	}
}

// tick checks and runs due jobs.
func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, job := range s.jobs {
		if !job.Enabled {
			continue
		}

		if now.After(job.NextRun) || now.Equal(job.NextRun) {
			// Run the job
			go s.executeJob(ctx, job)

			// Calculate next run time
			job.NextRun = job.Schedule.Next(now)
		}
	}
}

// executeJob runs a single job.
func (s *Scheduler) executeJob(ctx context.Context, job *Job) {
	job.mu.Lock()
	if job.Running {
		job.mu.Unlock()
		logger.Warn("job already running, skipping", "id", job.ID)
		return
	}
	job.Running = true
	job.LastRun = time.Now()
	job.RunCount++
	job.mu.Unlock()

	logger.Info("job started", "id", job.ID)

	// Create timeout context if specified
	runCtx := ctx
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}

	// Execute the job
	err := job.Func(runCtx)

	job.mu.Lock()
	job.Running = false
	if err != nil {
		job.LastError = err
		job.ErrorCount++
		logger.Error("job failed", "id", job.ID, "err", err)
	} else {
		job.LastError = nil
		logger.Info("job completed", "id", job.ID)
	}
	job.mu.Unlock()
}

// RunNow immediately runs a job regardless of schedule.
func (s *Scheduler) RunNow(ctx context.Context, id string) error {
	s.mu.RLock()
	job, ok := s.jobs[id]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	go s.executeJob(ctx, job)
	return nil
}
