package thread

import (
	"context"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

const preThinkTimeout = 10 * time.Second

// isPreThinkSession reports whether the session key is a pre-think sibling.
func isPreThinkSession(key string) bool {
	return strings.HasSuffix(key, session.PreThinkSessionSuffix)
}

// fastModelConfigured reports whether the "fast" specialty is mapped to a
// concrete provider/model in the current config (with hot-reload).
func fastModelConfigured(cfg *ThreadConfig) bool {
	models := cfg.Models
	if cfg.ModelsFn != nil {
		models = cfg.ModelsFn()
	}
	mc, ok := models["fast"]
	return ok && mc != nil
}

// preThinkAction runs the pre-think agent synchronously and returns its
// analysis to use as the action hint for the main wake payload.
// Returns "" if pre-think is not configured, the input is empty, or it fails.
//
// Caller must guard with sysmsg.IsUserVisibleSource — pre-think only runs
// for real user messages, not system wakes.
func preThinkAction(ctx context.Context, t *Thread, userMsg string) string {
	if strings.TrimSpace(userMsg) == "" {
		return ""
	}
	if isPreThinkSession(t.sessionKey) || isRephraseSession(t.sessionKey) {
		return ""
	}
	if !fastModelConfigured(t.cfg()) {
		return ""
	}

	ch := make(chan string, 1)
	preThinkKey := t.sessionKey + session.PreThinkSessionSuffix
	t.mgr.Wake(preThinkKey, &WakeMessage{
		Source:    WakePreThink,
		Message:   userMsg,
		AgentName: "pre-think",
		OnComplete: func(response string) {
			ch <- response
		},
	})

	select {
	case result := <-ch:
		result = strings.TrimSpace(result)
		if result != "" {
			logger.Info("pre-think completed",
				"sessionKey", t.sessionKey,
				"resultLen", len(result),
			)
		}
		return result
	case <-time.After(preThinkTimeout):
		logger.Warn("pre-think timeout", "sessionKey", t.sessionKey)
		return ""
	case <-ctx.Done():
		return ""
	}
}
