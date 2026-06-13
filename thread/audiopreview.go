package thread

import (
	"context"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

const (
	// audioPreviewTimeout bounds the upfront transcription. Audio chat turns
	// run larger than pre-think's text-only analysis, so the budget is wider.
	audioPreviewTimeout = 60 * time.Second
	// audioPreviewChatEntries is how many recent chat.jsonl lines feed the
	// transcriber as conversational context (disambiguates names / jargon).
	audioPreviewChatEntries = 8
	audioPreviewAgent       = "audio-preview"
)

// audioPreviewConfigured reports whether the audio-preview path can run: the
// audio-preview agent template is loaded AND the "audio" specialty resolves to
// a concrete provider/model. Mirrors pre-think's fastModelConfigured guard — if
// audio is not set up, the caller silently skips the preview rather than
// routing a voice clip to a non-audio model.
func (mgr *Manager) audioPreviewConfigured() bool {
	cfg := mgr.cfg
	if cfg == nil || cfg.Agents == nil || cfg.Agents.Def(audioPreviewAgent) == nil {
		return false
	}
	rules := cfg.Models
	if cfg.ModelsFn != nil {
		rules = cfg.ModelsFn()
	}
	return config.FindModelRule(rules, config.ModelRuleSpecialty, "audio") != nil
}

// AudioPreview transcribes a voice clip by waking the stateless audio-preview
// sibling session with the audio attached natively in user content (no
// read_file). It blocks for the transcription and returns it as plain text.
//
// audioMarker is a single "<<media:audio/...:/abs/path>>" marker. parentKey is
// the user's session key; the sibling runs at parentKey+":audiopreview" and
// inherits the parent's USER.md (language background) via parentSessionKey.
//
// Returns "" when audio is not configured, on timeout, or on failure — the
// caller proceeds without a preview rather than blocking the message. The
// transcription is delivered only via OnComplete; an explicit drop sink ensures
// it can never leak to the user's channel even though the sibling key shares
// the parent's channel prefix.
func (mgr *Manager) AudioPreview(ctx context.Context, parentKey, audioMarker string) string {
	if mgr == nil || strings.TrimSpace(audioMarker) == "" {
		return ""
	}
	if !mgr.audioPreviewConfigured() {
		return ""
	}

	ch := make(chan string, 1)
	key := parentKey + session.AudioPreviewSessionSuffix
	recentChat := session.ReadRecentChat(mgr.SessionDir(parentKey), audioPreviewChatEntries)

	mgr.Wake(key, &WakeMessage{
		Source:     WakeAudioPreview,
		Message:    "Transcribe the attached audio.",
		Media:      []string{audioMarker},
		AgentName:  audioPreviewAgent,
		RecentChat: recentChat,
		Sink: Sink{
			Label: "audio-preview session — transcription returns via callback, never delivered to a channel",
			Send:  func(context.Context, string) error { return nil },
		},
		OnComplete: func(response string) { ch <- response },
	})

	start := time.Now()
	select {
	case result := <-ch:
		result = strings.TrimSpace(result)
		logger.Info("audio preview completed",
			"sessionKey", parentKey,
			"durationMs", time.Since(start).Milliseconds(),
			"len", len(result),
		)
		return result
	case <-time.After(audioPreviewTimeout):
		logger.Warn("audio preview timeout", "sessionKey", parentKey)
		return ""
	case <-ctx.Done():
		return ""
	}
}
