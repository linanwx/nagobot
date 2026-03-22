package thread

import (
	"context"
	"fmt"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// hookTimeout is the maximum duration a single hook is allowed to run.
const hookTimeout = 5 * time.Second

// turnHook runs during message construction and returns messages to inject in
// the current turn.
type turnHook func(ctx context.Context, tc turnContext) []string

// turnContext carries read-only request context for hook evaluation.
type turnContext struct {
	ThreadID string

	SessionKey  string
	SessionPath string
	UserMessage string

	SessionEstimatedTokens int
	RequestEstimatedTokens int
	ContextWindowTokens    int
	WarnToken              int
}

// registerHook adds a hook for this thread.
func (t *Thread) registerHook(h turnHook) {
	if h == nil {
		return
	}
	t.mu.Lock()
	t.hooks = append(t.hooks, h)
	t.mu.Unlock()
}

func (t *Thread) runHooks(ctx context.Context, tc turnContext) []string {
	t.mu.Lock()
	hooks := make([]turnHook, len(t.hooks))
	copy(hooks, t.hooks)
	t.mu.Unlock()

	var injected []string
	for i, h := range hooks {
		hookCtx, cancel := context.WithTimeout(ctx, hookTimeout)
		result := runSingleHook(hookCtx, h, tc, i)
		cancel()
		if len(result) > 0 {
			injected = append(injected, result...)
		}
	}
	return injected
}

// runSingleHook executes a hook in a goroutine with timeout and panic recovery.
func runSingleHook(ctx context.Context, h turnHook, tc turnContext, index int) []string {
	type hookResult struct {
		messages []string
		panicked bool
		panicVal any
	}

	ch := make(chan hookResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- hookResult{panicked: true, panicVal: r}
			}
		}()
		ch <- hookResult{messages: h(ctx, tc)}
	}()

	select {
	case <-ctx.Done():
		logger.Warn("hook timed out, skipping",
			"hookIndex", index,
			"threadID", tc.ThreadID,
			"sessionKey", tc.SessionKey,
			"timeout", hookTimeout,
		)
		return nil
	case res := <-ch:
		if res.panicked {
			logger.Warn("hook panicked, skipping",
				"hookIndex", index,
				"threadID", tc.ThreadID,
				"sessionKey", tc.SessionKey,
				"panic", fmt.Sprintf("%v", res.panicVal),
			)
			return nil
		}
		return res.messages
	}
}
