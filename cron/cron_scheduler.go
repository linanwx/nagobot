package cron

import (
	"time"

	"github.com/linanwx/nagobot/logger"
)

func (s *Scheduler) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.readStore()
	if err != nil {
		return err
	}

	// Safe swap: only reset in-memory schedules after the new store is parsed successfully.
	s.resetLocked()

	// Schedule store jobs first (high priority, persisted).
	now := time.Now().UTC()
	dirty := false
	for _, raw := range list {
		job := Normalize(raw)
		ok, expired := ValidateStored(job, now)
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

	// Schedule seed jobs (low priority — skip if store already has same ID).
	for _, raw := range s.seedJobs {
		job := Normalize(raw)
		if _, overridden := s.jobs[job.ID]; overridden {
			continue
		}
		ok, _ := ValidateStored(job, now)
		if !ok {
			continue
		}
		cancel, err := s.scheduleLocked(job)
		if err != nil {
			logger.Warn("failed to schedule seed job", "id", job.ID, "err", err)
			continue
		}
		if cancel != nil {
			s.cancels[job.ID] = cancel
		}
		// NOT added to s.jobs — seeds are not persisted
	}

	if dirty {
		if err := s.saveLocked(); err != nil {
			logger.Warn("failed to save cron store after pruning expired at jobs", "err", err)
		}
	}
	return nil
}

func (s *Scheduler) Start() {
	if s.cron != nil {
		s.cron.Start()
	}
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	s.resetLocked()
	s.mu.Unlock()

	if s.cron != nil {
		_ = s.cron.Shutdown()
	}
}
