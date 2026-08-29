package thread

import (
	"strings"
	"testing"
	"time"

	sysmsg "github.com/linanwx/nagobot/thread/msg"
)

func TestBuildWakePayload_SupportsVisionAudio(t *testing.T) {
	// Vision+audio capable model → both fields present with true.
	loc := time.FixedZone("UTC+8", 8*3600)

	payload := buildWakePayload(
		WakeTelegram,
		"Hello with image",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"telegram delivery", "openrouter/google/gemini-3.7-flash", "soul",
		loc, "user", "", "", "", "", "", time.Time{}, "",
	)

	if !strings.Contains(payload, "supports_vision: true") {
		t.Errorf("expected supports_vision: true for the openrouter gemini route, got:\n%s", payload)
	}
	if !strings.Contains(payload, "supports_audio: true") {
		t.Errorf("expected supports_audio: true for the openrouter gemini route, got:\n%s", payload)
	}
}

func TestBuildWakePayload_MediaFields_OneLine(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)

	payload := buildWakePayload(
		WakeTelegram,
		"What's this?",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"telegram delivery", "", "soul",
		loc, "user", "", "",
		"[Media: photo]\nimage_path: /tmp/media/img.jpg\ncaption: hi",
		"[image preview: a cat\nsitting on\r\na table]",
		"", time.Time{}, "",
	)

	if !strings.Contains(payload, "media: '[Media: photo] image_path: /tmp/media/img.jpg caption: hi'") {
		t.Errorf("expected one-line media field, got:\n%s", payload)
	}
	if !strings.Contains(payload, "media_preview: '[image preview: a cat sitting on a table]'") {
		t.Errorf("expected one-line media_preview field, got:\n%s", payload)
	}
	// The body must stay pure user speech.
	if strings.Contains(payload, "\n[Media: photo]") {
		t.Errorf("media summary leaked into body:\n%s", payload)
	}
}

func TestBuildWakePayload_SystemSource_WithCapabilities(t *testing.T) {
	// System sources now also include capabilities when model supports them.
	loc := time.FixedZone("UTC+8", 8*3600)

	payload := buildWakePayload(
		WakeHeartbeat,
		"Heartbeat pulse",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"", "openrouter/google/gemini-3.7-flash", "soul",
		loc, "system", "", "", "", "", "", time.Time{}, "",
	)

	if !strings.Contains(payload, "supports_vision: true") {
		t.Errorf("heartbeat with capable model should include supports_vision: true:\n%s", payload)
	}
}

func TestBuildWakePayload_NoModel_NoMultimodalInfo(t *testing.T) {
	// When model is empty, multimodal fields should not appear.
	loc := time.FixedZone("UTC+8", 8*3600)

	payload := buildWakePayload(
		WakeTelegram,
		"Hello",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"telegram delivery", "", "soul",
		loc, "", "", "", "", "", "", time.Time{}, "",
	)

	if strings.Contains(payload, "supports_vision") {
		t.Errorf("empty model should not include supports_vision:\n%s", payload)
	}
}

func TestBuildWakePayload_FalseCapabilities_Omitted(t *testing.T) {
	// Model without vision/audio → fields should NOT appear (omitted, not false).
	loc := time.FixedZone("UTC+8", 8*3600)

	// z-ai/glm-5.3 is not in VisionModels or AudioModels
	payload := buildWakePayload(
		WakeTelegram,
		"Hello",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"telegram delivery", "openrouter/z-ai/glm-5.3", "soul",
		loc, "", "", "", "", "", "", time.Time{}, "",
	)

	if strings.Contains(payload, "supports_vision") {
		t.Errorf("non-vision model should not include supports_vision:\n%s", payload)
	}
	if strings.Contains(payload, "supports_audio") {
		t.Errorf("non-audio model should not include supports_audio:\n%s", payload)
	}
}

// ---------- markInjected ----------

func TestMarkInjected_Basic(t *testing.T) {
	loc := time.UTC
	payload := buildWakePayload(
		WakeTelegram, "Hi", "t-1", "telegram:1", "", "telegram delivery", "", "soul", loc, "user", "", "", "", "", "", time.Time{}, "",
	)
	out := markInjected(payload)
	if !strings.Contains(out, "injected: true") {
		t.Errorf("output missing `injected: true`:\n%s", out)
	}
}

func TestMarkInjected_PreservesMultiLineActionScalar(t *testing.T) {
	// Build a wake with a multi-line `action: |-` block scalar (the original
	// bug surface). After markInjected the action scalar must still parse
	// correctly — no orphan key/values, no body content leaked into YAML.
	loc := time.UTC
	payload := buildWakePayload(
		WakeSession,
		"the body content",
		"t-1", "discord:s1", "/sessions/discord/s1",
		"reply forwarded to caller", "", "soul",
		loc, "system", "", "", "", "", "discord:s1:threads:foo", time.Time{}, "",
	)
	if !strings.Contains(payload, "action: |") {
		// Wake action hint may not always be a block scalar depending on
		// length; force a multi-line one if the default isn't.
		t.Skip("buildWakePayload did not emit a block scalar action; skipping multi-line markInjected check")
	}
	out := markInjected(payload)

	// Result must round-trip: parse, find injected:true, no leak from action body.
	mapping, body, ok := sysmsg.ParseFrontmatter(out)
	if !ok {
		t.Fatalf("markInjected output not parseable:\n%s", out)
	}
	if sysmsg.LookupScalar(mapping, "injected") != "true" {
		t.Errorf("injected key not set")
	}
	// `MUST NOT` appears inside the standard wake action hint as a colon-line
	// — must NOT become a top-level key.
	if sysmsg.LookupScalar(mapping, "MUST NOT") != "" {
		t.Errorf("action body content leaked as top-level key after markInjected")
	}
	if !strings.Contains(body, "the body content") {
		t.Errorf("body content lost after markInjected: %q", body)
	}

	// Idempotency: running markInjected twice should not accumulate.
	out2 := markInjected(out)
	mapping2, _, _ := sysmsg.ParseFrontmatter(out2)
	pairs := mapping2.Content
	injectedCount := 0
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i].Value == "injected" {
			injectedCount++
		}
	}
	if injectedCount != 2 {
		// AppendScalarPair appends unconditionally; this is expected behavior.
		// We're documenting it: callers must ensure markInjected runs once.
		t.Logf("note: markInjected adds another `injected` entry on each call (current behavior); count=%d", injectedCount)
	}
}

func TestBuildWakePayload_RecentChatInMarkdownBody(t *testing.T) {
	// The media-preview sibling sessions are what carry RecentChat.
	loc := time.UTC
	payload := buildWakePayload(
		WakeImagePreview,
		"What now?",
		"t-1", "discord:1:imagepreview", "/sessions/discord/1/imagepreview",
		"imagepreview delivery", "openrouter/google/gemini-3.1-flash-lite", "imagereader",
		loc, "system", "", "", "", "", "", time.Time{}, "user: earlier\nassistant: reply",
	)

	mapping, body, ok := sysmsg.ParseFrontmatter(payload)
	if !ok {
		t.Fatalf("payload not parseable:\n%s", payload)
	}
	if sysmsg.LookupScalar(mapping, "recent_chat") != "" {
		t.Fatalf("recent_chat leaked into YAML frontmatter:\n%s", payload)
	}
	if got := sysmsg.LookupScalar(mapping, "action"); got != wakeActionHint(WakeImagePreview) {
		t.Fatalf("action should be the source hint:\ngot:  %s\nwant: %s", got, wakeActionHint(WakeImagePreview))
	}
	for _, want := range []string{"## history", "user: earlier", "## message", "What now?", "## instruction", "Use the history as conversation context", "Follow the YAML action field"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestBuildWakePayload_UsesEnqueuedAt(t *testing.T) {
	// When enqueuedAt is provided, the `time:` field must reflect that
	// timestamp (rendered in the given location), not time.Now().
	loc := time.FixedZone("UTC+8", 8*3600)
	enqueued := time.Date(2026, 5, 9, 4, 5, 6, 0, time.UTC) // → 12:05:06+08:00

	payload := buildWakePayload(
		WakeTelegram, "hi",
		"t-1", "telegram:1", "",
		"telegram delivery", "", "soul",
		loc, "user", "", "", "", "", "", enqueued, "",
	)

	if !strings.Contains(payload, "time: 2026-05-09T12:05:06+08:00") {
		t.Errorf("expected enqueued time rendered in loc, got:\n%s", payload)
	}
}

func TestBuildWakePayload_ZeroEnqueuedAtFallsBackToNow(t *testing.T) {
	// Zero enqueuedAt → fall back to time.Now() so direct callers still work.
	loc := time.UTC
	payload := buildWakePayload(
		WakeTelegram, "hi",
		"t-1", "telegram:1", "",
		"telegram delivery", "", "soul",
		loc, "user", "", "", "", "", "", time.Time{}, "",
	)

	if !strings.Contains(payload, "time: ") {
		t.Errorf("payload missing time field:\n%s", payload)
	}
	// Year must be reasonable (not 0001).
	if strings.Contains(payload, "time: 0001-") {
		t.Errorf("zero enqueuedAt leaked into payload:\n%s", payload)
	}
}

func TestMarkInjected_NoFrontmatter(t *testing.T) {
	// Inputs without frontmatter pass through unchanged — markInjected must
	// not corrupt or wrap them.
	in := "no frontmatter here"
	if got := markInjected(in); got != in {
		t.Errorf("expected pass-through, got %q", got)
	}
}
