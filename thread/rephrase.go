package thread

import (
	"context"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
)

// isRephraseSession reports whether the session key is a rephrase sibling.
func isRephraseSession(key string) bool {
	return strings.HasSuffix(key, session.RephraseSessionSuffix)
}

// rephraseCompoundSink builds the sink for a rephrase session's wake.
// It delivers the rephrased content to the original channel sink and
// updates the parent session's last assistant message.
func rephraseCompoundSink(originalSink sysmsg.Sink, parentSessionKey string, sessions *session.Manager) sysmsg.Sink {
	return sysmsg.Sink{
		Label:     originalSink.Label,
		Chunkable: originalSink.Chunkable,
		React:     originalSink.React,
		Send: func(ctx context.Context, rephrased string) error {
			rephrased = strings.TrimSpace(rephrased)
			if rephrased == "" {
				return nil
			}
			// 1. Update parent session: Content→rephrased, original→OriginalContent.
			if err := sessions.RephraseLastAssistant(parentSessionKey, rephrased); err != nil {
				logger.Warn("rephrase: failed to update parent session",
					"parentSession", parentSessionKey, "err", err)
			}
			// 2. Deliver rephrased content to the user.
			return originalSink.Send(ctx, rephrased)
		},
	}
}
