package channel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// stubDocChannel records SendDoc calls and lets tests inject errors.
type stubDocChannel struct {
	mu       sync.Mutex
	received []DocRef
	err      error
}

func (s *stubDocChannel) Name() string                                   { return "stub-doc" }
func (s *stubDocChannel) Start(ctx context.Context) error                { return nil }
func (s *stubDocChannel) Stop() error                                    { return nil }
func (s *stubDocChannel) Send(ctx context.Context, resp *Response) error { return nil }
func (s *stubDocChannel) Messages() <-chan *Message                      { return nil }
func (s *stubDocChannel) SendDoc(ctx context.Context, replyTo string, ref DocRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, ref)
	return s.err
}

func writeTempDoc(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write temp doc: %v", err)
	}
	return path
}

func TestDispatchDocRefs_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	docPath := writeTempDoc(t, dir, "report.pdf", "%PDF-1.4 fake content")

	stub := &stubDocChannel{}
	text := "see [Q1 report](" + docPath + ") attached"
	dispatchDocRefs(context.Background(), stub, "reply-target", text, dir)

	if len(stub.received) != 1 {
		t.Fatalf("got %d refs, want 1", len(stub.received))
	}
	got := stub.received[0]
	if got.Path != docPath {
		t.Errorf("Path = %q, want %q", got.Path, docPath)
	}
	if got.Label != "Q1 report" {
		t.Errorf("Label = %q, want %q", got.Label, "Q1 report")
	}
	if got.Name != "report.pdf" {
		t.Errorf("Name = %q, want report.pdf", got.Name)
	}
	if got.Mime == "" {
		t.Errorf("Mime should be set, got empty")
	}
}

func TestDispatchDocRefs_RelativePathResolvedAgainstWorkspace(t *testing.T) {
	ws := t.TempDir()
	mediaDir := filepath.Join(ws, "media")
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempDoc(t, mediaDir, "rel.txt", "hello")

	stub := &stubDocChannel{}
	text := "[rel](media/rel.txt)"
	dispatchDocRefs(context.Background(), stub, "x", text, ws)

	if len(stub.received) != 1 {
		t.Fatalf("got %d refs, want 1", len(stub.received))
	}
	wantPath := filepath.Join(mediaDir, "rel.txt")
	if stub.received[0].Path != wantPath {
		t.Errorf("Path = %q, want %q", stub.received[0].Path, wantPath)
	}
}

func TestDispatchDocRefs_MissingFileSkipped(t *testing.T) {
	stub := &stubDocChannel{}
	text := "[ghost](/nonexistent/path.pdf)"
	dispatchDocRefs(context.Background(), stub, "x", text, "")
	if len(stub.received) != 0 {
		t.Errorf("expected no refs delivered, got %d", len(stub.received))
	}
}

func TestDispatchDocRefs_DirectorySkipped(t *testing.T) {
	dir := t.TempDir()
	stub := &stubDocChannel{}
	text := "[folder](" + dir + ")"
	dispatchDocRefs(context.Background(), stub, "x", text, "")
	if len(stub.received) != 0 {
		t.Errorf("expected directory to be rejected, got %d refs", len(stub.received))
	}
}

func TestDispatchDocRefs_EmptyFileSkipped(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty.pdf")
	if err := os.WriteFile(emptyPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	stub := &stubDocChannel{}
	text := "[empty](" + emptyPath + ")"
	dispatchDocRefs(context.Background(), stub, "x", text, "")
	if len(stub.received) != 0 {
		t.Errorf("expected empty file to be rejected, got %d refs", len(stub.received))
	}
}

func TestDispatchDocRefs_URLSkipped(t *testing.T) {
	stub := &stubDocChannel{}
	text := "[Discord](https://discord.com)"
	dispatchDocRefs(context.Background(), stub, "x", text, "")
	if len(stub.received) != 0 {
		t.Errorf("expected URL to be rejected, got %d refs", len(stub.received))
	}
}

func TestDispatchDocRefs_ImageSyntaxNotClaimed(t *testing.T) {
	dir := t.TempDir()
	docPath := writeTempDoc(t, dir, "x.png", "fake image bytes")
	stub := &stubDocChannel{}
	text := "![alt](" + docPath + ")"
	dispatchDocRefs(context.Background(), stub, "x", text, dir)
	if len(stub.received) != 0 {
		t.Errorf("expected image syntax to be ignored by doc dispatcher, got %d refs", len(stub.received))
	}
}

func TestDispatchDocRefs_SendDocErrorDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	docPath := writeTempDoc(t, dir, "x.pdf", "%PDF-1.4")
	stub := &stubDocChannel{err: errors.New("boom")}
	text := "[x](" + docPath + ")"
	dispatchDocRefs(context.Background(), stub, "x", text, dir)
}

func TestDispatchDocRefs_NonDocSenderChannelIsNoop(t *testing.T) {
	noop := &noopChannel{}
	dispatchDocRefs(context.Background(), noop, "x", "[x](/whatever.pdf)", "")
}

func TestManagerSendResponse_DispatchesDocs(t *testing.T) {
	dir := t.TempDir()
	docPath := writeTempDoc(t, dir, "m.pdf", "%PDF-1.4 content")

	stub := &stubDocChannel{}
	mgr := NewManager()
	mgr.WorkspaceFn = func() string { return dir }
	mgr.Register(stub)

	resp := &Response{
		Text:    "look [m](" + docPath + ")",
		ReplyTo: "target",
	}
	if err := mgr.SendResponse(context.Background(), "stub-doc", resp); err != nil {
		t.Fatalf("SendResponse: %v", err)
	}
	if len(stub.received) != 1 {
		t.Fatalf("got %d doc refs, want 1", len(stub.received))
	}
}
