package channel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
	"github.com/linanwx/nagobot/thread/msg"
)

// CronChannel wraps a cron.Scheduler as a Channel. Each fired job wakes a
// target session via onDirectWake — independent mode runs cron:<ID> with the
// configured agent; inject mode wakes WakeSession directly without overriding
// its agent. Send is a no-op; responses are controlled by the session's own
// dispatch() calls.
type CronChannel struct {
	storePath    string
	seedJobs     []cronpkg.Job // config-defined seeds
	scheduler    *cronpkg.Scheduler
	messages     chan *Message
	done         chan struct{}
	onDirectWake func(sessionKey string, source msg.WakeSource, message, agentName, deliveryLabel string)
}

// NewCronChannel creates a CronChannel from config.
func NewCronChannel(cfg *config.Config) *CronChannel {
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		logger.Warn("cron channel: failed to get workspace path", "err", err)
	}
	ch := &CronChannel{
		storePath: filepath.Join(workspace, "system", "cron.jsonl"),
		seedJobs:  cfg.Cron,
		messages:  make(chan *Message, 64),
		done:      make(chan struct{}),
	}
	return ch
}

func (c *CronChannel) Name() string { return "cron" }

// SetDirectWake sets a callback invoked on every cron fire. The callback is
// responsible for waking sessionKey with the given message. agentName is
// non-empty for independent mode (sets/overrides session agent meta);
// empty for inject mode (preserves target session's existing agent).
// deliveryLabel carries mode-specific guidance that appears in the wake
// frontmatter so the LLM knows where it should dispatch results.
func (c *CronChannel) SetDirectWake(fn func(sessionKey string, source msg.WakeSource, message, agentName, deliveryLabel string)) {
	c.onDirectWake = fn
}

// FindJob looks up a cron job by ID. Returns zero Job and false if the
// scheduler hasn't started or the job doesn't exist.
func (c *CronChannel) FindJob(id string) (cronpkg.Job, bool) {
	if c.scheduler == nil {
		// Scheduler not started yet; check seed jobs as fallback.
		for _, j := range c.seedJobs {
			if j.ID == id {
				return j, true
			}
		}
		return cronpkg.Job{}, false
	}
	return c.scheduler.FindJob(id)
}

// AddJob delegates to the underlying scheduler.
func (c *CronChannel) AddJob(job cronpkg.Job) error {
	if c.scheduler == nil {
		return fmt.Errorf("cron scheduler not started")
	}
	return c.scheduler.AddJob(job)
}

func (c *CronChannel) Start(ctx context.Context) error {
	factory := func(job *cronpkg.Job) (string, error) {
		if job == nil {
			return "", nil
		}
		if c.onDirectWake == nil {
			// Fallback: push through Messages() channel (legacy, not expected in normal wiring).
			c.messages <- c.buildMessage(job)
			return "", nil
		}

		jobID := strings.TrimSpace(job.ID)
		if jobID == "" {
			jobID = "job"
		}
		target := strings.TrimSpace(job.WakeSession)
		task := strings.TrimSpace(job.Task)

		if job.DirectWake {
			// Inject mode: must have target session; agent is ignored (preserve target's meta).
			if target == "" {
				logger.Warn("cron: direct_wake without wake_session, skipping", "id", jobID)
				return "", nil
			}
			delivery := "you were woken by cron (inject mode). Caller is cron — output to caller is dropped. " +
				"Use dispatch(to=user) to message the channel user, or dispatch(to=session, session_key=...) " +
				"to forward elsewhere."
			c.onDirectWake(target, msg.WakeCron, task, "", delivery)
			return "", nil
		}

		// Independent mode: run in cron:<jobID> session with configured agent.
		sessionKey := "cron:" + jobID
		agent := strings.TrimSpace(job.Agent)
		var delivery string
		if target != "" {
			delivery = "you were woken by cron (independent mode). Caller is cron — output to caller is dropped. " +
				"After completing your task, dispatch(to=session, session_key=\"" + target + "\") to deliver results."
		} else {
			delivery = "you were woken by cron (independent mode). Caller is cron — output to caller is dropped. " +
				"No delivery target configured; use dispatch explicitly if you need to forward results."
			logger.Warn("cron: independent mode without wake_session (silent execution)", "id", jobID)
		}
		c.onDirectWake(sessionKey, msg.WakeCron, task, agent, delivery)
		return "", nil
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
		return msg.BuildSystemMessage("cron", nil, "scheduled cron task triggered")
	}

	atTime := ""
	if job.AtTime != nil {
		atTime = job.AtTime.UTC().Format(time.RFC3339)
	}

	return msg.BuildSystemMessage("cron", map[string]string{
		"id":           strings.TrimSpace(job.ID),
		"kind":         strings.TrimSpace(job.Kind),
		"expr":         strings.TrimSpace(job.Expr),
		"at_time":      atTime,
		"task":         strings.TrimSpace(job.Task),
		"agent":        strings.TrimSpace(job.Agent),
		"wake_session": strings.TrimSpace(job.WakeSession),
		"created_at":   job.CreatedAt.UTC().Format(time.RFC3339),
	}, "scheduled cron task triggered")
}
