package cron

import (
	"fmt"
	"time"

	"github.com/linanwx/nagobot/logger"
)

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

func (s *Scheduler) resetLocked() {
	for _, cancel := range s.cancels {
		cancel()
	}
	s.jobs = make(map[string]Job)
	s.cancels = make(map[string]func())
}
