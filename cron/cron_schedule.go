package cron

import (
	"fmt"
	"strings"

	gocron "github.com/go-co-op/gocron/v2"
	"github.com/linanwx/nagobot/logger"
)

func (s *Scheduler) scheduleLocked(job Job) (func(), error) {
	if s.cron == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	switch job.Kind {
	case JobKindCron:
		registered, err := s.cron.NewJob(
			gocron.CronJob(job.Expr, false),
			gocron.NewTask(func(j Job) {
				if s.factory == nil {
					return
				}
				if _, runErr := s.factory(&j); runErr != nil {
					logger.Warn("cron job execution failed", "id", j.ID, "err", runErr)
				}
			}, job),
			gocron.WithName(job.ID),
		)
		if err != nil {
			return nil, err
		}
		return func() { _ = s.cron.RemoveJob(registered.ID()) }, nil

	case JobKindAt:
		registered, err := s.cron.NewJob(
			gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(job.AtTime)),
			gocron.NewTask(func(j Job) {
				if s.factory != nil {
					jc := j
					if _, err := s.factory(&jc); err != nil {
						logger.Warn("at job execution failed", "id", j.ID, "err", err)
					}
				}

				s.mu.Lock()
				s.finalizeAtJobLocked(j.ID)
				s.mu.Unlock()
			}, job),
			gocron.WithName(job.ID),
		)
		if err != nil {
			return nil, err
		}
		return func() { _ = s.cron.RemoveJob(registered.ID()) }, nil
	}

	return nil, fmt.Errorf("unsupported job kind: %s", job.Kind)
}

func (s *Scheduler) finalizeAtJobLocked(jobID string) {
	if strings.TrimSpace(jobID) == "" {
		return
	}
	s.unscheduleLocked(jobID)
	delete(s.jobs, jobID)
	if err := s.saveLocked(); err != nil {
		logger.Warn("failed to persist cron store after at job execution", "id", jobID, "err", err)
	}
}

func (s *Scheduler) unscheduleLocked(id string) {
	if cancel, ok := s.cancels[id]; ok {
		cancel()
		delete(s.cancels, id)
	}
}

func (s *Scheduler) resetLocked() {
	for id, cancel := range s.cancels {
		cancel()
		delete(s.cancels, id)
	}
	s.jobs = make(map[string]Job)
	s.cancels = make(map[string]func())
}
