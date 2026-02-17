package channel

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
)

// CronChannel wraps a cron.Scheduler as a Channel. Each fired job produces
// a Message on the Messages() channel. Send is a no-op — responses are
// delivered via thread sinks.
type CronChannel struct {
	storePath string
	seedJobs  []cronpkg.Job // config-defined seeds
	scheduler *cronpkg.Scheduler
	messages  chan *Message
	done      chan struct{}
}

// NewCronChannel creates a CronChannel from config.
func NewCronChannel(cfg *config.Config) Channel {
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		logger.Warn("cron channel: failed to get workspace path", "err", err)
	}
	ch := &CronChannel{
		storePath: filepath.Join(workspace, "cron.jsonl"),
		seedJobs:  cfg.Cron,
		messages:  make(chan *Message, 64),
		done:      make(chan struct{}),
	}
	return ch
}

func (c *CronChannel) Name() string { return "cron" }

func (c *CronChannel) Start(ctx context.Context) error {
	factory := func(job *cronpkg.Job) (string, error) {
		c.messages <- c.buildMessage(job)
		return "", nil // fire-and-forget
	}

	sch, err := cronpkg.NewScheduler(c.storePath, factory, c.seedJobs)
	if err != nil {
		return fmt.Errorf("failed to create cron scheduler: %w", err)
	}
	c.scheduler = sch
	if err := c.scheduler.Load(); err != nil {
		return fmt.Errorf("failed to load cron jobs: %w", err)
	}
	c.scheduler.Start()

	// Periodic reload goroutine.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.done:
				return
			case <-time.After(time.Minute):
				if err := c.scheduler.Load(); err != nil {
					logger.Warn("failed to reload cron jobs", "err", err)
				}
			}
		}
	}()

	return nil
}

func (c *CronChannel) Stop() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	if c.scheduler != nil {
		c.scheduler.Stop()
	}
	return nil
}

func (c *CronChannel) Send(_ context.Context, _ *Response) error {
	return nil // no-op: responses go through thread sinks
}

func (c *CronChannel) Messages() <-chan *Message {
	return c.messages
}

func (c *CronChannel) buildMessage(job *cronpkg.Job) *Message {
	jobID := "job"
	if job != nil && strings.TrimSpace(job.ID) != "" {
		jobID = strings.TrimSpace(job.ID)
	}

	suffix := thread.RandomHex(4)
	if suffix == "" {
		suffix = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	msgID := fmt.Sprintf("cron-%s-%s", jobID, suffix)

	text := buildCronStartMessage(job)
	if job != nil && strings.TrimSpace(job.Task) != "" {
		task := strings.TrimSpace(job.Task)
		if text != "" {
			text += "\n\n" + task
		} else {
			text = task
		}
	}

	metadata := map[string]string{
		"job_id": jobID,
	}
	if job != nil {
		metadata["agent"] = strings.TrimSpace(job.Agent)
		metadata["task"] = strings.TrimSpace(job.Task)
		metadata["wake_session"] = strings.TrimSpace(job.WakeSession)
		metadata["silent"] = strconv.FormatBool(job.Silent)
	}

	return &Message{
		ID:        msgID,
		ChannelID: "cron:" + jobID,
		Text:      text,
		Metadata:  metadata,
	}
}

func buildCronStartMessage(job *cronpkg.Job) string {
	if job == nil {
		return "[Cron wake notice]\nReason: scheduled cron task triggered."
	}

	atTime := ""
	if job.AtTime != nil {
		atTime = job.AtTime.UTC().Format(time.RFC3339)
	}

	return fmt.Sprintf(
		"[Cron wake notice]\nReason: scheduled cron task triggered.\nRaw job config:\n- id: %s\n- kind: %s\n- expr: %s\n- at_time: %s\n- task: %s\n- agent: %s\n- wake_session: %s\n- silent: %t\n- created_at: %s",
		strings.TrimSpace(job.ID),
		strings.TrimSpace(job.Kind),
		strings.TrimSpace(job.Expr),
		atTime,
		strings.TrimSpace(job.Task),
		strings.TrimSpace(job.Agent),
		strings.TrimSpace(job.WakeSession),
		job.Silent,
		job.CreatedAt.UTC().Format(time.RFC3339),
	)
}
