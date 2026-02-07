package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
)

func startCronRuntime(ctx context.Context, rt *threadRuntime, threadMgr *thread.Manager) (*cronpkg.Scheduler, error) {
	cronStorePath := filepath.Join(rt.workspace, "cron.yaml")
	scheduler := cronpkg.NewScheduler(cronStorePath, func(job *cronpkg.Job) (string, error) {
		ag := rt.soulAgent
		if job != nil && rt.threadConfig != nil && rt.threadConfig.Agents != nil {
			if agentName := strings.TrimSpace(job.Agent); agentName != "" {
				if named := rt.threadConfig.Agents.Get(agentName); named != nil {
					ag = named
				}
			}
		}

		task := ""
		if job != nil {
			task = strings.TrimSpace(job.Task)
		}
		ag = thread.WrapAgentTaskPlaceholder(ag, task)
		t := thread.NewChannel(rt.threadConfig, ag, buildCronSessionKey(job), nil)
		result, runErr := t.Run(ctx, task)

		if job != nil && !job.Silent && runErr == nil && strings.TrimSpace(result) != "" {
			creatorKey := strings.TrimSpace(job.CreatorSessionKey)
			if creatorKey == "" {
				logger.Warn("cron job has no creator session key; skipping wake", "id", job.ID)
			} else if wakeErr := threadMgr.WakeThread(ctx, creatorKey, buildCronWakeMessage(job, result)); wakeErr != nil {
				logger.Warn("failed to wake creator thread for cron result", "id", job.ID, "creatorSessionKey", creatorKey, "err", wakeErr)
			}
		}

		return result, runErr
	})

	if err := scheduler.Load(); err != nil {
		return nil, fmt.Errorf("failed to load cron jobs: %w", err)
	}
	scheduler.Start()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				if err := scheduler.Load(); err != nil {
					logger.Warn("failed to reload cron jobs", "err", err)
				}
			}
		}
	}()

	return scheduler, nil
}

func buildCronSessionKey(job *cronpkg.Job) string {
	jobID := "job"
	if job != nil && strings.TrimSpace(job.ID) != "" {
		jobID = strings.TrimSpace(job.ID)
	}

	now := time.Now().UTC()
	suffix := thread.RandomHex(4)
	if suffix == "" {
		suffix = fmt.Sprintf("%d", now.UnixNano())
	}
	return fmt.Sprintf(
		"cron:%s:%s:%s-%s",
		jobID,
		now.Format("2006-01-02"),
		now.Format("20060102T150405Z"),
		suffix,
	)
}

func buildCronWakeMessage(job *cronpkg.Job, result string) string {
	jobID := "job"
	if job != nil && strings.TrimSpace(job.ID) != "" {
		jobID = strings.TrimSpace(job.ID)
	}
	return fmt.Sprintf("[Cron job completed]\n- id: %s\n- result:\n%s", jobID, strings.TrimSpace(result))
}
