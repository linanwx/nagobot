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

	// lastUserActive reports when a real human last spoke ANYWHERE in this
	// deployment. Injected, because computing it means scanning every
	// session.jsonl and channel/ has no business knowing how sessions are
	// stored — the same seam SetDirectWake uses.
	lastUserActive func() (time.Time, error)
}

// builtinCronQuietWindow is how long a deployment must go without a real human
// message before its built-in maintenance jobs stop firing.
const builtinCronQuietWindow = 24 * time.Hour

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

// SetLastUserActive injects the deployment-wide "when did a human last speak"
// clock that gates the built-in maintenance jobs. Leaving it unset disables the
// gate entirely; see skipIdleBuiltin for why that is the safe default.
func (c *CronChannel) SetLastUserActive(fn func() (time.Time, error)) {
	c.lastUserActive = fn
}

// shouldSkipJob reports whether this fire should be dropped because the job is
// one of the built-ins and no real human has spoken anywhere in this deployment
// for builtinCronQuietWindow.
//
// The built-in jobs exist to digest what humans did: tidyup files their work,
// people-knowledge and world-knowledge run off the conversations. On a
// deployment nobody is using, they run anyway. Measured across the four live
// deployments on 2026-08-02, three had gone 2, 3 and 9 days without a human
// message while all three jobs kept firing nightly — roughly 19 wasted LLM
// turns on the quietest one.
//
// The window is checked GLOBALLY, never per session. These jobs run in their own
// cron:<id> sessions, which have no human by construction, so a per-session
// check would skip all of them forever.
//
// Custom jobs are never gated. They are the user's own schedule, and several on
// the live deployments push weekly reports to people who are precisely NOT daily
// chat users — gating those would silently stop the reports for their intended
// audience.
//
// Fails OPEN. No injected clock, or a clock that errors, runs the job: a missing
// wiring or an unreadable session tree must not quietly disable maintenance. A
// zero timestamp with no error is different — it means the scan ran and found no
// human at all, which is idle by definition and correctly skips.
func (c *CronChannel) shouldSkipJob(jobID string) bool {
	if !config.IsBuiltinCronJob(jobID) {
		return false
	}
	if c.lastUserActive == nil {
		return false
	}
	last, err := c.lastUserActive()
	if err != nil {
		logger.Warn("cron: cannot determine last user activity, running built-in job anyway",
			"id", jobID, "err", err)
		return false
	}
	if !last.IsZero() && time.Since(last) < builtinCronQuietWindow {
		return false
	}
	logger.Info("cron: skipping built-in job, no human activity in this deployment",
		"id", jobID,
		"window", builtinCronQuietWindow,
		"lastUserActiveAt", lastActiveLabel(last),
	)
	return true
}

func lastActiveLabel(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format(time.RFC3339) + " (" + time.Since(t).Round(time.Minute).String() + " ago)"
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

// AddJob delegates to the underlying scheduler. Returns whether an existing
// job with the same ID was replaced.
func (c *CronChannel) AddJob(job cronpkg.Job) (bool, error) {
	if c.scheduler == nil {
		return false, fmt.Errorf("cron scheduler not started")
	}
	return c.scheduler.AddJob(job)
}

// RemoveJob delegates to the underlying scheduler.
func (c *CronChannel) RemoveJob(id string) (bool, error) {
	if c.scheduler == nil {
		return false, fmt.Errorf("cron scheduler not started")
	}
	return c.scheduler.RemoveJob(id)
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
		// Gate the built-in maintenance jobs on recent human activity. Checked
		// here rather than at schedule time because the answer changes between
		// the schedule being set and the job firing — the whole point is that a
		// deployment goes quiet while its cron entries stay put.
		if c.shouldSkipJob(jobID) {
			return "", nil
		}
		target := strings.TrimSpace(job.WakeSession)
		task := strings.TrimSpace(job.Task)

		if job.DirectWake {
			// Inject mode: must have target session; agent is ignored (preserve target's meta).
			if target == "" {
				logger.Warn("cron: direct_wake without wake_session, skipping", "id", jobID)
				return "", nil
			}
			// Inject mode targets an arbitrary session and the target is never
			// validated, so only the thread knows whether it has a human. On a
			// user-facing target the thread's contentSink routes plain content
			// to the channel user and replaces this label with that sink's own;
			// what remains here is therefore what a NON-user-facing target
			// (cron:/subagent/sibling) sees, where the caller sink drops. This
			// used to assert channel-user delivery unconditionally, which was
			// backwards for every --direct-wake into such a session. The
			// WakeCron action hint carries the conditional form.
			delivery := "you were woken by cron (inject mode). Caller is cron — there is nobody to reply to, " +
				"and this session has no channel user of its own, so plain reply text is dropped. " +
				"Use dispatch(to=session, params={session_key: ...}) to deliver results, or dispatch({}) to end silently."
			c.onDirectWake(target, msg.WakeCron, task, "", delivery)
			return "", nil
		}

		// Independent mode: run in cron:<jobID> session with configured agent.
		sessionKey := "cron:" + jobID
		agent := strings.TrimSpace(job.Agent)
		var delivery string
		if target != "" {
			delivery = "you were woken by cron (independent mode). Caller is cron — output to caller is dropped. " +
				"After completing your task, dispatch(to=session, params={session_key: \"" + target + "\"}) to deliver results."
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
