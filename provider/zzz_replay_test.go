package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestReplayRealGLMStream(t *testing.T) {
	sse, err := os.ReadFile("/private/tmp/claude-501/-Users-linan-Documents-nagobot--claude-worktrees-android-remote-control-tool-e7c277/f5a82783-e535-4925-9b5d-8d662425e37a/scratchpad/glm_stream.sse")
	if err != nil {
		t.Skip(err)
	}
	t.Logf("captured stream: %d bytes, %d reasoning_content deltas",
		len(sse), strings.Count(string(sse), "\"reasoning_content\":\""))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sse)
	}))
	defer srv.Close()

	p := newZhipuProvider("zhipu-cn", "k", srv.URL, srv.URL, "glm-5.3-flash", "glm-5.3-flash", 4000, 1)
	res, err := p.Chat(context.Background(), &Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := res.Wait()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PARSED: ReasoningContent=%d chars, Content=%d chars, ReasoningTokens=%d",
		len(resp.ReasoningContent), len(resp.Content), resp.Usage.ReasoningTokens)
	if resp.ReasoningContent == "" {
		t.Error("ReasoningContent is EMPTY although the stream carries reasoning_content deltas")
	}
}
