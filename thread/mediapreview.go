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
	// mediaPreviewTimeout bounds an upfront preview turn. Native media chat
	// turns run larger than pre-think's text-only analysis, so the budget is
	// wider.
	mediaPreviewTimeout = 60 * time.Second
	// mediaPreviewChatEntries is how many recent chat.jsonl lines feed the
	// previewer as conversational context (disambiguates names / jargon /
	// what "the screenshot" refers to).
	mediaPreviewChatEntries = 8
)

// previewSpec describes one upfront-preview variant. Audio and image share the
// exact control flow (configured-check → wake stateless sibling with the media
// attached natively → block on OnComplete behind a drop sink); only these
// fields differ.
type previewSpec struct {
	agent     string     // agent template name (audio-preview / image-preview)
	specialty string     // model-routing specialty the agent requires (audio / image)
	suffix    string     // sibling session key suffix
	source    WakeSource // wake source tag
	message   string     // wake payload text
	label     string     // log label
}

var audioPreviewSpec = previewSpec{
	agent:     "audio-preview",
	specialty: "audio",
	suffix:    session.AudioPreviewSessionSuffix,
	source:    WakeAudioPreview,
	message:   "Transcribe the attached audio.",
	label:     "audio",
}

var imagePreviewSpec = previewSpec{
	agent:     "image-preview",
	specialty: "image",
	suffix:    session.ImagePreviewSessionSuffix,
	source:    WakeImagePreview,
	message:   "Describe the attached image.",
	label:     "image",
}

// previewConfigured reports whether a preview variant can run: its agent
// template is loaded AND its specialty resolves to a concrete provider/model.
// Mirrors pre-think's fastModelConfigured guard — if the variant is not set
// up, the caller silently skips the preview rather than routing media to a
// model that cannot consume it.
func (mgr *Manager) previewConfigured(spec previewSpec) bool {
	cfg := mgr.cfg
	if cfg == nil || cfg.Agents == nil || cfg.Agents.Def(spec.agent) == nil {
		return false
	}
	rules := cfg.Models
	if cfg.ModelsFn != nil {
		rules = cfg.ModelsFn()
	}
	return config.FindModelRule(rules, config.ModelRuleSpecialty, spec.specialty) != nil
}

// mediaPreview generates an upfront text preview of a media file by waking the
// stateless preview sibling with the file attached natively in user content
// (no read_file). It blocks for the result and returns it as plain text.
//
// marker is a single "<<media:mime:/abs/path>>" marker. parentKey is the user's
// session key; the sibling runs at parentKey+spec.suffix and inherits the
// parent's USER.md (context) via parentSessionKey.
//
// Returns "" when the variant is not configured, on timeout, or on failure —
// the caller proceeds without a preview. The result is delivered only via
// OnComplete; an explicit drop sink ensures it can never leak to the user's
// channel even though the sibling key shares the parent's channel prefix.
func (mgr *Manager) mediaPreview(ctx context.Context, parentKey, marker string, spec previewSpec) string {
	if mgr == nil || strings.TrimSpace(marker) == "" {
		return ""
	}
	if !mgr.previewConfigured(spec) {
		return ""
	}

	ch := make(chan string, 1)
	key := parentKey + spec.suffix
	recentChat := session.ReadRecentChat(mgr.SessionDir(parentKey), mediaPreviewChatEntries, mgr.locationFor(parentKey))

	mgr.Wake(key, &WakeMessage{
		Source:     spec.source,
		Message:    spec.message,
		Media:      []string{marker},
		AgentName:  spec.agent,
		RecentChat: recentChat,
		Sink: Sink{
			Label: spec.label + "-preview session — result returns via callback, never delivered to a channel",
			Send:  func(context.Context, string) error { return nil },
		},
		OnComplete: func(response string) { ch <- response },
	})

	start := time.Now()
	select {
	case result := <-ch:
		result = strings.TrimSpace(result)
		logger.Info("media preview completed",
			"kind", spec.label,
			"sessionKey", parentKey,
			"durationMs", time.Since(start).Milliseconds(),
			"len", len(result),
		)
		return result
	case <-time.After(mediaPreviewTimeout):
		logger.Warn("media preview timeout", "kind", spec.label, "sessionKey", parentKey)
		return ""
	case <-ctx.Done():
		return ""
	}
}

// AudioPreview transcribes a voice clip via the audio-preview agent.
func (mgr *Manager) AudioPreview(ctx context.Context, parentKey, marker string) string {
	return mgr.mediaPreview(ctx, parentKey, marker, audioPreviewSpec)
}

// ImagePreview describes an image via the image-preview agent.
func (mgr *Manager) ImagePreview(ctx context.Context, parentKey, marker string) string {
	return mgr.mediaPreview(ctx, parentKey, marker, imagePreviewSpec)
}
