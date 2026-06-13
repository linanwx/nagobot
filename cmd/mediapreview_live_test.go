package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/media"
	"github.com/linanwx/nagobot/thread"
)

// liveMediaPreview is the shared real-API harness for the preview e2e tests: it
// builds the live thread manager from the local config, starts its run loop,
// and invokes call (AudioPreview / ImagePreview) which wakes the preview
// sibling with the file attached natively (Media field) and blocks for the
// result. Gated by each caller's env-var check so normal `go test` never hits
// the API or spends tokens.
func liveMediaPreview(t *testing.T, call func(*thread.Manager, context.Context) string) string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	mgr, _, _, err := buildThreadManager(cfg, true)
	if err != nil {
		t.Fatalf("buildThreadManager: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	go mgr.Run(ctx)
	return call(mgr, ctx)
}

// TestAudioPreviewLive — real voice clip → audio-preview agent → transcription.
//
//	NAGOBOT_LIVE_AUDIO=/Users/.../media/audio-....ogg go test ./cmd -run TestAudioPreviewLive -v
func TestAudioPreviewLive(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("NAGOBOT_LIVE_AUDIO"))
	if path == "" {
		t.Skip("set NAGOBOT_LIVE_AUDIO=<abs .ogg path> to run the live audio-preview e2e")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("audio file not found: %v", err)
	}
	marker := fmt.Sprintf("<<media:%s:%s>>", media.DetectAudioMime(path), path)
	got := liveMediaPreview(t, func(m *thread.Manager, ctx context.Context) string {
		return m.AudioPreview(ctx, "livetest-audio", marker)
	})
	if got == "" {
		t.Fatal("AudioPreview returned empty — audio not configured, model not audio-capable, or call failed (check logs)")
	}
	t.Logf("transcription (%d chars): %s", len(got), got)
}

// TestImagePreviewLive — real image → image-preview agent → description.
//
//	NAGOBOT_LIVE_IMAGE=/Users/.../media/photo.jpg go test ./cmd -run TestImagePreviewLive -v
func TestImagePreviewLive(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("NAGOBOT_LIVE_IMAGE"))
	if path == "" {
		t.Skip("set NAGOBOT_LIVE_IMAGE=<abs image path> to run the live image-preview e2e")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("image file not found: %v", err)
	}
	marker := fmt.Sprintf("<<media:%s:%s>>", media.DetectImageMime(path), path)
	got := liveMediaPreview(t, func(m *thread.Manager, ctx context.Context) string {
		return m.ImagePreview(ctx, "livetest-image", marker)
	})
	if got == "" {
		t.Fatal("ImagePreview returned empty — image not configured, model not vision-capable, or call failed (check logs)")
	}
	t.Logf("description (%d chars): %s", len(got), got)
}
