package channel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// stubImageChannel records SendImage calls and lets tests inject errors.
type stubImageChannel struct {
	mu       sync.Mutex
	received []ImageRef
	err      error
}

func (s *stubImageChannel) Name() string                                   { return "stub" }
func (s *stubImageChannel) Start(ctx context.Context) error                { return nil }
func (s *stubImageChannel) Stop() error                                    { return nil }
func (s *stubImageChannel) Send(ctx context.Context, resp *Response) error { return nil }
func (s *stubImageChannel) Messages() <-chan *Message                      { return nil }
func (s *stubImageChannel) SendImage(ctx context.Context, replyTo string, ref ImageRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, ref)
	return s.err
}

// writeTempImage writes a 1x1 PNG (valid magic bytes) so DetectFileType
// classifies the result as an image without needing an external fixture.
func writeTempImage(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	return path
}

func TestDispatchImageRefs_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	imgPath := writeTempImage(t, dir, "pic.png")

	stub := &stubImageChannel{}
	text := "see ![cat](" + imgPath + ") here"
	dispatchImageRefs(context.Background(), stub, "reply-target", text, dir)

	if len(stub.received) != 1 {
		t.Fatalf("got %d refs, want 1", len(stub.received))
	}
	got := stub.received[0]
	if got.Path != imgPath {
		t.Errorf("Path = %q, want %q", got.Path, imgPath)
	}
	if got.Alt != "cat" {
		t.Errorf("Alt = %q, want %q", got.Alt, "cat")
	}
	if got.Mime != "image/png" {
		t.Errorf("Mime = %q, want image/png", got.Mime)
	}
}

func TestDispatchImageRefs_RelativePathResolvedAgainstWorkspace(t *testing.T) {
	ws := t.TempDir()
	mediaDir := filepath.Join(ws, "media")
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempImage(t, mediaDir, "rel.png")

	stub := &stubImageChannel{}
	text := "![rel](media/rel.png)"
	dispatchImageRefs(context.Background(), stub, "x", text, ws)

	if len(stub.received) != 1 {
		t.Fatalf("got %d refs, want 1", len(stub.received))
	}
	wantPath := filepath.Join(mediaDir, "rel.png")
	if stub.received[0].Path != wantPath {
		t.Errorf("Path = %q, want %q", stub.received[0].Path, wantPath)
	}
}

func TestDispatchImageRefs_MissingFileSkipped(t *testing.T) {
	stub := &stubImageChannel{}
	text := "![ghost](/nonexistent/path.png)"
	dispatchImageRefs(context.Background(), stub, "x", text, "")
	if len(stub.received) != 0 {
		t.Errorf("expected no refs delivered, got %d", len(stub.received))
	}
}

func TestDispatchImageRefs_NonImageFileSkipped(t *testing.T) {
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "fake.png")
	if err := os.WriteFile(txtPath, []byte("not actually an image"), 0644); err != nil {
		t.Fatal(err)
	}
	stub := &stubImageChannel{}
	text := "![nope](" + txtPath + ")"
	dispatchImageRefs(context.Background(), stub, "x", text, "")
	if len(stub.received) != 0 {
		t.Errorf("expected text file to be rejected, got %d refs", len(stub.received))
	}
}

func TestDispatchImageRefs_SendImageErrorDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	imgPath := writeTempImage(t, dir, "pic.png")
	stub := &stubImageChannel{err: errors.New("boom")}
	text := "![x](" + imgPath + ")"
	dispatchImageRefs(context.Background(), stub, "x", text, dir)
}

func TestDispatchImageRefs_NonImageSenderChannelIsNoop(t *testing.T) {
	noop := &noopChannel{}
	dispatchImageRefs(context.Background(), noop, "x", "![x](/whatever.png)", "")
}

type noopChannel struct{}

func (noopChannel) Name() string                                   { return "noop" }
func (noopChannel) Start(ctx context.Context) error                { return nil }
func (noopChannel) Stop() error                                    { return nil }
func (noopChannel) Send(ctx context.Context, resp *Response) error { return nil }
func (noopChannel) Messages() <-chan *Message                      { return nil }
