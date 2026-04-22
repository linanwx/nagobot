package thread

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Fixed reference time so payload tests don't depend on wall clock.
var testPostTime = time.Date(2026, 4, 22, 10, 30, 0, 0, time.FixedZone("CST", 8*3600))

// newThreadWithLoc creates a minimal Thread with the given location for
// post-hook tests. The location path goes through cfg().SessionTimezoneFor,
// which is absent here, so t.location() falls back to time.Now().Location().
// Tests that need a deterministic time value call buildImplicitCallerForwardPayload
// directly with a fixed timestamp instead of going through the hook.
func newThreadWithLoc() *Thread {
	return &Thread{
		id:         "test-thread",
		sessionKey: "discord:1480577226356789",
	}
}

func TestImplicitCallerForwardHook_UserFacingEmitsHint(t *testing.T) {
	th := newThreadWithLoc()
	hook := th.implicitCallerForwardHook()

	result := hook(context.Background(), postTurnContext{
		ThreadID:         "th",
		SessionKey:       "discord:1480577226356789",
		WakeSource:       WakeSession,
		CallerSessionKey: "telegram:42",
		IsUserFacing:     true,
		SinkSuppressed:   false,
		ResponseNonEmpty: true,
	})

	if len(result) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(result))
	}
	payload := result[0]
	if !strings.Contains(payload, "source: implicit-caller-forward") {
		t.Errorf("payload missing source frontmatter: %q", payload)
	}
	if !strings.Contains(payload, "sender: system") {
		t.Errorf("payload missing sender: system: %q", payload)
	}
	if !strings.Contains(payload, "injected: true") {
		t.Errorf("payload missing injected: true: %q", payload)
	}
	if !strings.Contains(payload, "forwarded to caller session telegram:42") {
		t.Errorf("payload missing peerKey: %q", payload)
	}
	if !strings.Contains(payload, "May also dispatch to user next time") {
		t.Errorf("user-facing payload must include user-hint sentence: %q", payload)
	}
}

func TestImplicitCallerForwardHook_NonUserFacingOmitsHint(t *testing.T) {
	th := newThreadWithLoc()
	hook := th.implicitCallerForwardHook()

	result := hook(context.Background(), postTurnContext{
		ThreadID:         "th",
		SessionKey:       "discord:123:threads:t1",
		WakeSource:       WakeSession,
		CallerSessionKey: "discord:123",
		IsUserFacing:     false,
		ResponseNonEmpty: true,
	})

	if len(result) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(result))
	}
	payload := result[0]
	if !strings.Contains(payload, "forwarded to caller session discord:123") {
		t.Errorf("payload missing peerKey: %q", payload)
	}
	if strings.Contains(payload, "May also dispatch to user next time") {
		t.Errorf("non-user-facing payload must NOT include user-hint sentence: %q", payload)
	}
}

func TestImplicitCallerForwardHook_WrongSourceReturnsNil(t *testing.T) {
	th := newThreadWithLoc()
	hook := th.implicitCallerForwardHook()

	cases := []WakeSource{WakeTelegram, WakeDiscord, WakeCron, WakeHeartbeat, WakeCompression, WakeRephrase}
	for _, src := range cases {
		result := hook(context.Background(), postTurnContext{
			WakeSource:       src,
			CallerSessionKey: "telegram:42",
			IsUserFacing:     true,
			ResponseNonEmpty: true,
		})
		if result != nil {
			t.Errorf("source=%s must return nil, got %v", src, result)
		}
	}
}

func TestImplicitCallerForwardHook_EmptyCallerKeyReturnsNil(t *testing.T) {
	th := newThreadWithLoc()
	hook := th.implicitCallerForwardHook()

	result := hook(context.Background(), postTurnContext{
		WakeSource:       WakeSession,
		CallerSessionKey: "",
		IsUserFacing:     true,
		ResponseNonEmpty: true,
	})
	if result != nil {
		t.Errorf("empty callerKey must return nil, got %v", result)
	}
}

func TestImplicitCallerForwardHook_SinkSuppressedReturnsNil(t *testing.T) {
	th := newThreadWithLoc()
	hook := th.implicitCallerForwardHook()

	result := hook(context.Background(), postTurnContext{
		WakeSource:       WakeSession,
		CallerSessionKey: "telegram:42",
		IsUserFacing:     true,
		SinkSuppressed:   true,
		ResponseNonEmpty: true,
	})
	if result != nil {
		t.Errorf("suppressed sink must return nil (explicit dispatch handled it), got %v", result)
	}
}

func TestImplicitCallerForwardHook_EmptyResponseReturnsNil(t *testing.T) {
	th := newThreadWithLoc()
	hook := th.implicitCallerForwardHook()

	result := hook(context.Background(), postTurnContext{
		WakeSource:       WakeSession,
		CallerSessionKey: "telegram:42",
		IsUserFacing:     true,
		SinkSuppressed:   false,
		ResponseNonEmpty: false,
	})
	if result != nil {
		t.Errorf("empty response must return nil (nothing was routed), got %v", result)
	}
}

func TestBuildImplicitCallerForwardPayload_Structure(t *testing.T) {
	payload := buildImplicitCallerForwardPayload("telegram:42", true, testPostTime)

	lines := strings.Split(payload, "\n")
	if lines[0] != "---" {
		t.Errorf("line 0 = %q, want %q", lines[0], "---")
	}
	headerEnd := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			headerEnd = i
			break
		}
	}
	if headerEnd < 0 {
		t.Fatalf("no closing --- found in payload: %q", payload)
	}
	// Required frontmatter fields must all appear between the two --- lines.
	frontmatter := strings.Join(lines[1:headerEnd], "\n")
	mustContain := []string{
		"source: implicit-caller-forward",
		"sender: system",
		"injected: true",
		"time: 2026-04-22T10:30:00+08:00",
	}
	for _, needle := range mustContain {
		if !strings.Contains(frontmatter, needle) {
			t.Errorf("frontmatter missing %q; got:\n%s", needle, frontmatter)
		}
	}
	// Body starts after a blank line following the closing ---.
	if lines[headerEnd+1] != "" {
		t.Errorf("expected blank line after closing ---, got %q", lines[headerEnd+1])
	}
	body := strings.Join(lines[headerEnd+2:], "\n")
	if !strings.HasPrefix(body, "Default output reply detected.") {
		t.Errorf("body must start with the reminder sentence, got: %q", body)
	}
}

func TestRunPostHooks_Timeout(t *testing.T) {
	th := &Thread{}
	th.registerPostHook(func(ctx context.Context, _ postTurnContext) []string {
		<-ctx.Done()
		return nil
	})

	start := time.Now()
	result := th.runPostHooks(context.Background(), postTurnContext{ThreadID: "test"})
	elapsed := time.Since(start)

	if len(result) != 0 {
		t.Errorf("expected no results from timed-out hook, got %v", result)
	}
	if elapsed > hookTimeout+time.Second {
		t.Errorf("runPostHooks took %v, expected ~%v", elapsed, hookTimeout)
	}
}

func TestRunPostHooks_PanicRecovery(t *testing.T) {
	th := &Thread{}
	th.registerPostHook(func(_ context.Context, _ postTurnContext) []string {
		return []string{"pre"}
	})
	th.registerPostHook(func(_ context.Context, _ postTurnContext) []string {
		panic("boom")
	})
	th.registerPostHook(func(_ context.Context, _ postTurnContext) []string {
		return []string{"post"}
	})

	result := th.runPostHooks(context.Background(), postTurnContext{ThreadID: "test"})
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(result), result)
	}
	if result[0] != "pre" || result[1] != "post" {
		t.Errorf("results = %v, want [pre post]", result)
	}
}

func TestRunPostHooks_NoHooksReturnsNil(t *testing.T) {
	th := &Thread{}
	result := th.runPostHooks(context.Background(), postTurnContext{})
	if result != nil {
		t.Errorf("expected nil for no hooks, got %v", result)
	}
}
