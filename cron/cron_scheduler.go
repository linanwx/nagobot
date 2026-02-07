package cron

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

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

func (s *Scheduler) Add(id, expr, task, agent, creatorSessionKey string, silent bool) error {
	return s.add(Job{
		ID:                strings.TrimSpace(id),
		Kind:              JobKindCron,
		Expr:              strings.TrimSpace(expr),
		Task:              strings.TrimSpace(task),
		Agent:             strings.TrimSpace(agent),
		CreatorSessionKey: strings.TrimSpace(creatorSessionKey),
		Silent:            silent,
		Enabled:           true,
		CreatedAt:         time.Now().UTC(),
	})
}

func (s *Scheduler) AddAt(id string, atTime time.Time, task, agent, creatorSessionKey string, silent bool) error {
	return s.add(Job{
		ID:                strings.TrimSpace(id),
		Kind:              JobKindAt,
		AtTime:            atTime.UTC(),
		Task:              strings.TrimSpace(task),
		Agent:             strings.TrimSpace(agent),
		CreatorSessionKey: strings.TrimSpace(creatorSessionKey),
		Silent:            silent,
		Enabled:           true,
		CreatedAt:         time.Now().UTC(),
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
