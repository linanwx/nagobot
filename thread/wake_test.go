package thread

import (
	"strings"
	"testing"
	"time"
)

func TestBuildWakePayload_SupportsVisionAudio(t *testing.T) {
	// Test that user-visible sources include supports_vision and supports_audio
	// when the model string is in "provider/model" format.
	loc := time.FixedZone("UTC+8", 8*3600)

	// Use a known vision-capable model from the gemini provider.
	payload := buildWakePayload(
		WakeTelegram,
		"Hello with image",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"telegram delivery", "gemini/gemini-3-flash-preview", "soul",
		loc,
	)

	if !strings.Contains(payload, "supports_vision:") {
		t.Errorf("expected supports_vision field in wake header for user-visible source, got:\n%s", payload)
	}
	if !strings.Contains(payload, "supports_audio:") {
		t.Errorf("expected supports_audio field in wake header for user-visible source, got:\n%s", payload)
	}
}

func TestBuildWakePayload_SystemSource_NoMultimodalInfo(t *testing.T) {
	// System sources should NOT include supports_vision/supports_audio.
	loc := time.FixedZone("UTC+8", 8*3600)

	payload := buildWakePayload(
		WakeHeartbeat,
		"Heartbeat pulse",
		"thread-1", "telegram:123", "/tmp/sessions/telegram:123",
		"", "gemini/gemini-3-flash-preview", "soul",
		loc,
	)

	if strings.Contains(payload, "supports_vision") {
		t.Errorf("system source should not include supports_vision:\n%s", payload)
	}
	if strings.Contains(payload, "supports_audio") {
		t.Errorf("system source should not include supports_audio:\n%s", payload)
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
		loc,
	)

	if strings.Contains(payload, "supports_vision") {
		t.Errorf("empty model should not include supports_vision:\n%s", payload)
	}
}
