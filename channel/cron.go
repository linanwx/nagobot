package channel

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
)

// CronChannel wraps a cron.Scheduler as a Channel. Each fired job produces
// a Message on the Messages() channel. Send is a no-op — responses are
// delivered via thread sinks.
type CronChannel struct {
	storePath string
	scheduler *cronpkg.Scheduler
	messages  chan *Message
	done      chan struct{}
}

// NewCronChannel creates a CronChannel backed by a cron.yaml in the workspace.
func NewCronChannel(workspace string) *CronChannel {
	ch := &CronChannel{
		storePath: filepath.Join(workspace, "cron.yaml"),
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

	c.scheduler = cronpkg.NewScheduler(c.storePath, factory)
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
		metadata["creator_session_key"] = strings.TrimSpace(job.CreatorSessionKey)
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
	if !job.AtTime.IsZero() {
		atTime = job.AtTime.UTC().Format(time.RFC3339)
	}

	return fmt.Sprintf(
		"[Cron wake notice]\nReason: scheduled cron task triggered.\nRaw job config:\n- id: %s\n- kind: %s\n- expr: %s\n- at_time: %s\n- task: %s\n- agent: %s\n- creator_session_key: %s\n- silent: %t\n- created_at: %s",
		strings.TrimSpace(job.ID),
		strings.TrimSpace(job.Kind),
		strings.TrimSpace(job.Expr),
		atTime,
		strings.TrimSpace(job.Task),
		strings.TrimSpace(job.Agent),
		strings.TrimSpace(job.CreatorSessionKey),
		job.Silent,
		job.CreatedAt.UTC().Format(time.RFC3339),
	)
}
