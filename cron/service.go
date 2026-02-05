package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
	"github.com/linanwx/nagobot/logger"
	"gopkg.in/yaml.v3"
)

// JobHandler handles execution for a due cron job.
type JobHandler func(ctx context.Context, job Job) error

// Service manages scheduled jobs loaded from a YAML config file.
type Service struct {
	configPath string
	jobs       []Job
	jobKeys    []string

	mu             sync.RWMutex
	watcher        *fsnotify.Watcher
	scheduler      gocron.Scheduler
	scheduledByKey map[string]gocron.Job
	stopCh         chan struct{}
	reconfigMu     sync.Mutex

	onJob    JobHandler
	started  bool
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewService creates a cron service from config path.
func NewService(configPath string) (*Service, error) {
	s := &Service{
		configPath:     configPath,
		stopCh:         make(chan struct{}),
		scheduledByKey: make(map[string]gocron.Job),
	}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) load() ([]Job, error) {
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return cfg.Jobs, nil
}

// LoadConfig reads jobs from a cron config file.
func LoadConfig(configPath string) ([]Job, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg.Jobs, nil
}

// SaveConfig writes jobs to a cron config file.
func SaveConfig(configPath string, jobs []Job) error {
	cfg := Config{Jobs: jobs}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

// Reload reloads jobs from disk and re-arms scheduling.
func (s *Service) Reload() error {
	s.reconfigMu.Lock()
	defer s.reconfigMu.Unlock()

	jobs, err := s.load()
	if err != nil {
		return err
	}
	return s.applyJobs(jobs)
}

// Start starts the scheduler loop.
func (s *Service) Start(onJob JobHandler) {
	s.reconfigMu.Lock()
	defer s.reconfigMu.Unlock()

	s.mu.Lock()
	s.onJob = onJob
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	scheduler := s.scheduler
	s.mu.Unlock()

	if scheduler != nil {
		scheduler.Start()
	}
}

// StartWatching watches cron config changes and auto-reloads.
func (s *Service) StartWatching() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.configPath)
	if dir == "" {
		dir = "."
	}
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return err
	}

	s.mu.Lock()
	if s.watcher != nil {
		s.mu.Unlock()
		watcher.Close()
		return nil
	}
	s.watcher = watcher
	s.mu.Unlock()

	configPathAbs, err := filepath.Abs(s.configPath)
	if err != nil {
		configPathAbs = s.configPath
	}
	configPathAbs = filepath.Clean(configPathAbs)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				eventPathAbs, absErr := filepath.Abs(event.Name)
				if absErr != nil {
					eventPathAbs = event.Name
				}
				if filepath.Clean(eventPathAbs) != configPathAbs {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					if err := s.Reload(); err != nil {
						logger.Warn("cron reload failed", "path", s.configPath, "err", err)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Warn("cron watcher error", "err", err)
			case <-s.stopCh:
				return
			}
		}
	}()

	return nil
}

// Stop stops watcher and scheduler loops.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})

	s.reconfigMu.Lock()
	s.mu.Lock()
	watcher := s.watcher
	s.watcher = nil
	scheduler := s.scheduler
	s.scheduler = nil
	s.scheduledByKey = make(map[string]gocron.Job)
	s.started = false
	s.mu.Unlock()
	s.reconfigMu.Unlock()

	if watcher != nil {
		_ = watcher.Close()
	}

	if scheduler != nil {
		_ = scheduler.Shutdown()
	}

	s.wg.Wait()
}

// Jobs returns current jobs sorted by nearest next run first.
func (s *Service) Jobs(includeDisabled bool) []Job {
	s.mu.RLock()
	jobsCopy := append([]Job(nil), s.jobs...)
	jobKeysCopy := append([]string(nil), s.jobKeys...)
	scheduler := s.scheduler
	s.mu.RUnlock()

	nextByKey := make(map[string]int64)
	if scheduler != nil {
		for _, scheduled := range scheduler.Jobs() {
			nextRun, err := scheduled.NextRun()
			if err != nil || nextRun.IsZero() {
				continue
			}
			nextByKey[scheduled.Name()] = nextRun.UnixMilli()
		}
	}

	type item struct {
		job     Job
		nextRun int64
	}
	items := make([]item, 0, len(jobsCopy))
	for i, job := range jobsCopy {
		if !includeDisabled && !job.Enabled {
			continue
		}
		nextRun := int64(0)
		if i < len(jobKeysCopy) {
			nextRun = nextByKey[jobKeysCopy[i]]
		}
		items = append(items, item{job: job, nextRun: nextRun})
	}

	sort.Slice(items, func(i, j int) bool {
		left := items[i].nextRun
		right := items[j].nextRun
		if left == 0 {
			return false
		}
		if right == 0 {
			return true
		}
		return left < right
	})

	out := make([]Job, 0, len(items))
	for _, it := range items {
		out = append(out, it.job)
	}
	return out
}

func (s *Service) applyJobs(jobs []Job) error {
	keys := makeJobKeys(jobs)
	newScheduler, scheduled, err := s.buildScheduler(jobs, keys)
	if err != nil {
		return err
	}

	s.mu.Lock()
	oldScheduler := s.scheduler
	wasStarted := s.started
	s.jobs = jobs
	s.jobKeys = keys
	s.scheduler = newScheduler
	s.scheduledByKey = scheduled
	s.mu.Unlock()

	if wasStarted {
		newScheduler.Start()
	}

	if oldScheduler != nil {
		_ = oldScheduler.Shutdown()
	}

	return nil
}

func (s *Service) buildScheduler(jobs []Job, keys []string) (gocron.Scheduler, map[string]gocron.Job, error) {
	scheduler, err := gocron.NewScheduler()
	if err != nil {
		return nil, nil, err
	}

	scheduled := make(map[string]gocron.Job)
	for i, job := range jobs {
		if !job.Enabled {
			continue
		}
		key := keys[i]

		jobDef, ok := buildJobDefinition(job.Schedule)
		if !ok {
			continue
		}

		registered, regErr := scheduler.NewJob(
			jobDef,
			gocron.NewTask(func(jobKey string) {
				s.executeJob(jobKey)
			}, key),
			gocron.WithName(key),
		)
		if regErr != nil {
			logger.Warn("failed to register cron job", "id", job.ID, "name", job.Name, "err", regErr)
			continue
		}
		scheduled[key] = registered
	}

	return scheduler, scheduled, nil
}

func buildJobDefinition(schedule Schedule) (gocron.JobDefinition, bool) {
	switch schedule.Kind {
	case ScheduleAt:
		if schedule.AtMs <= 0 || schedule.AtMs <= time.Now().UnixMilli() {
			return nil, false
		}
		return gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(time.UnixMilli(schedule.AtMs))), true

	case ScheduleEvery:
		if schedule.EveryMs <= 0 {
			logger.Warn("invalid interval schedule", "every_ms", schedule.EveryMs)
			return nil, false
		}
		return gocron.DurationJob(time.Duration(schedule.EveryMs) * time.Millisecond), true

	case ScheduleCron:
		if schedule.Expr == "" {
			return nil, false
		}
		expr := strings.TrimSpace(schedule.Expr)
		if schedule.Tz != "" {
			if _, err := time.LoadLocation(schedule.Tz); err != nil {
				logger.Warn("invalid cron timezone", "tz", schedule.Tz, "err", err)
				return nil, false
			}
			expr = fmt.Sprintf("CRON_TZ=%s %s", schedule.Tz, expr)
		}
		return gocron.CronJob(expr, false), true
	}

	logger.Warn("unsupported schedule kind", "kind", schedule.Kind)
	return nil, false
}

func (s *Service) executeJob(key string) {
	s.mu.RLock()
	idx := indexOfKey(s.jobKeys, key)
	if idx < 0 || idx >= len(s.jobs) {
		s.mu.RUnlock()
		return
	}
	job := s.jobs[idx]
	handler := s.onJob
	s.mu.RUnlock()

	if handler != nil {
		if err := handler(context.Background(), job); err != nil {
			logger.Error("cron job failed", "id", job.ID, "name", job.Name, "err", err)
		} else {
			logger.Info("cron job completed", "id", job.ID, "name", job.Name)
		}
	} else {
		logger.Info("cron job due (no handler)", "id", job.ID, "name", job.Name)
	}

	if job.Schedule.Kind == ScheduleAt {
		s.finalizeAtJob(key, job.DeleteAfterRun)
	}
}

func (s *Service) finalizeAtJob(key string, deleteAfterRun bool) {
	s.reconfigMu.Lock()
	defer s.reconfigMu.Unlock()

	s.mu.Lock()
	idx := indexOfKey(s.jobKeys, key)
	if idx < 0 {
		s.mu.Unlock()
		return
	}

	scheduler := s.scheduler
	scheduledJob, hasScheduled := s.scheduledByKey[key]
	delete(s.scheduledByKey, key)

	if deleteAfterRun {
		s.jobs = append(s.jobs[:idx], s.jobs[idx+1:]...)
		s.jobKeys = append(s.jobKeys[:idx], s.jobKeys[idx+1:]...)
	} else {
		s.jobs[idx].Enabled = false
	}
	s.mu.Unlock()

	if hasScheduled && scheduler != nil {
		_ = scheduler.RemoveJob(scheduledJob.ID())
	}
}

func makeJobKeys(jobs []Job) []string {
	keys := make([]string, len(jobs))
	counts := make(map[string]int)

	for i, job := range jobs {
		base := strings.TrimSpace(job.ID)
		if base == "" {
			base = strings.TrimSpace(job.Name)
		}
		if base == "" {
			base = fmt.Sprintf("job-%d", i+1)
		}

		counts[base]++
		if counts[base] == 1 {
			keys[i] = base
			continue
		}
		keys[i] = fmt.Sprintf("%s#%d", base, counts[base])
	}

	return keys
}

func indexOfKey(keys []string, key string) int {
	for i := range keys {
		if keys[i] == key {
			return i
		}
	}
	return -1
}
