package thread

import "testing"

// newMediaTestThread builds a minimal Thread whose model resolves (via the
// nil-agent fallback in currentModelSupports*) to the given provider/model.
func newMediaTestThread(provName, model string) *Thread {
	return &Thread{
		sessionKey: "telegram:1",
		mgr:        NewManager(&ThreadConfig{ProviderName: provName, ModelName: model}),
	}
}

func TestKeepSupportedMedia_AudioKeptOnAudioModel(t *testing.T) {
	th := newMediaTestThread("gemini", "gemini-3.1-flash-lite") // audio-capable
	in := []string{"<<media:audio/ogg:/tmp/v.ogg>>"}
	got := th.keepSupportedMedia(in)
	if len(got) != 1 || got[0] != in[0] {
		t.Errorf("audio marker should be kept on an audio-capable model, got %v", got)
	}
}

func TestKeepSupportedMedia_AudioDroppedOnNonAudioModel(t *testing.T) {
	th := newMediaTestThread("anthropic", "claude-haiku-4-5") // vision+pdf, NO audio
	got := th.keepSupportedMedia([]string{"<<media:audio/ogg:/tmp/v.ogg>>"})
	if len(got) != 0 {
		t.Errorf("audio marker should be dropped on a non-audio model, got %v", got)
	}
}

// TestKeepSupportedMedia_PerTypeDiscrimination proves the guard checks each
// marker against the matching capability, not a blanket allow/deny: a
// vision+pdf model keeps the image but drops the audio.
func TestKeepSupportedMedia_PerTypeDiscrimination(t *testing.T) {
	th := newMediaTestThread("anthropic", "claude-haiku-4-5")
	got := th.keepSupportedMedia([]string{
		"<<media:image/jpeg:/tmp/a.jpg>>",
		"<<media:audio/ogg:/tmp/b.ogg>>",
	})
	if len(got) != 1 || got[0] != "<<media:image/jpeg:/tmp/a.jpg>>" {
		t.Errorf("vision model should keep image and drop audio; got %v", got)
	}
}

func TestKeepSupportedMedia_MalformedDropped(t *testing.T) {
	th := newMediaTestThread("gemini", "gemini-3.1-flash-lite")
	if got := th.keepSupportedMedia([]string{"not-a-marker"}); len(got) != 0 {
		t.Errorf("malformed marker should be dropped, got %v", got)
	}
}

func TestKeepSupportedMedia_Empty(t *testing.T) {
	th := newMediaTestThread("gemini", "gemini-3.1-flash-lite")
	if got := th.keepSupportedMedia(nil); got != nil {
		t.Errorf("nil in should give nil out, got %v", got)
	}
}
