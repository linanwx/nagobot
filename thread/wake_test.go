package thread

import (
	"strings"
	"testing"
	"time"
)

func TestBuildWakePayload_SupportsVisionAudio(t *testing.T) {
	// Vision+audio capable model → both fields present with true.
	loc := time.FixedZone("UTC+8", 8*3600)

	payload := buildWakePayload(
		WakeTelegram,
		"Hello with image",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"telegram delivery", "gemini/gemini-3-flash-preview", "soul",
		loc, "user",
	)

	if !strings.Contains(payload, "supports_vision: true") {
		t.Errorf("expected supports_vision: true for gemini, got:\n%s", payload)
	}
	if !strings.Contains(payload, "supports_audio: true") {
		t.Errorf("expected supports_audio: true for gemini, got:\n%s", payload)
	}
}

func TestBuildWakePayload_SystemSource_WithCapabilities(t *testing.T) {
	// System sources now also include capabilities when model supports them.
	loc := time.FixedZone("UTC+8", 8*3600)

	payload := buildWakePayload(
		WakeHeartbeat,
		"Heartbeat pulse",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"", "gemini/gemini-3-flash-preview", "soul",
		loc, "system",
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
		loc, "",
	)

	if strings.Contains(payload, "supports_vision") {
		t.Errorf("empty model should not include supports_vision:\n%s", payload)
	}
}

func TestBuildWakePayload_FalseCapabilities_Omitted(t *testing.T) {
	// Model without vision/audio → fields should NOT appear (omitted, not false).
	loc := time.FixedZone("UTC+8", 8*3600)

	// z-ai/glm-5 is not in VisionModels or AudioModels
	payload := buildWakePayload(
		WakeTelegram,
		"Hello",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"telegram delivery", "openrouter/z-ai/glm-5", "soul",
		loc, "",
	)

	if strings.Contains(payload, "supports_vision") {
		t.Errorf("non-vision model should not include supports_vision:\n%s", payload)
	}
	if strings.Contains(payload, "supports_audio") {
		t.Errorf("non-audio model should not include supports_audio:\n%s", payload)
	}
}
