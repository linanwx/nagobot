package thread

import (
	"context"
	"testing"
	"time"
)

func TestRunHooks_Timeout(t *testing.T) {
	th := &Thread{}
	// Register a hook that blocks forever.
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		<-ctx.Done()
		return nil
	})

	ctx := context.Background()
	tc := turnContext{ThreadID: "test", SessionKey: "test-session"}

	start := time.Now()
	result := th.runHooks(ctx, tc)
	elapsed := time.Since(start)

	if len(result) != 0 {
		t.Errorf("expected no results from timed-out hook, got %v", result)
	}
	// Should complete within hookTimeout + generous margin (1s).
	if elapsed > hookTimeout+time.Second {
		t.Errorf("runHooks took %v, expected at most ~%v", elapsed, hookTimeout)
	}
}

func TestRunHooks_PanicRecovery(t *testing.T) {
	th := &Thread{}

	// Hook 1: returns a normal result.
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		return []string{"from-hook-1"}
	})

	// Hook 2: panics.
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		panic("test panic")
	})

	// Hook 3: returns a normal result after the panicking hook.
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		return []string{"from-hook-3"}
	})

	ctx := context.Background()
	tc := turnContext{ThreadID: "test", SessionKey: "test-session"}

	result := th.runHooks(ctx, tc)

	// The panicking hook should be skipped; results from hooks 1 and 3 should be present.
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(result), result)
	}
	if result[0] != "from-hook-1" {
		t.Errorf("result[0] = %q, want %q", result[0], "from-hook-1")
	}
	if result[1] != "from-hook-3" {
		t.Errorf("result[1] = %q, want %q", result[1], "from-hook-3")
	}
}

func TestRunHooks_NormalExecution(t *testing.T) {
	th := &Thread{}
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		return []string{"msg-a", "msg-b"}
	})
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		return nil
	})
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		return []string{"msg-c"}
	})

	ctx := context.Background()
	tc := turnContext{ThreadID: "test", SessionKey: "test-session"}

	result := th.runHooks(ctx, tc)

	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d: %v", len(result), result)
	}
	expected := []string{"msg-a", "msg-b", "msg-c"}
	for i, want := range expected {
		if result[i] != want {
			t.Errorf("result[%d] = %q, want %q", i, result[i], want)
		}
	}
}

func TestRunHooks_ParentContextCanceled(t *testing.T) {
	th := &Thread{}
	// A hook that respects context cancellation.
	th.registerHook(func(ctx context.Context, tc turnContext) []string {
		<-ctx.Done()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	tc := turnContext{ThreadID: "test", SessionKey: "test-session"}

	// Cancel the parent context immediately.
	cancel()

	start := time.Now()
	result := th.runHooks(ctx, tc)
	elapsed := time.Since(start)

	if len(result) != 0 {
		t.Errorf("expected no results, got %v", result)
	}
	// Should return almost immediately since parent is already canceled.
	if elapsed > time.Second {
		t.Errorf("runHooks took %v with canceled parent context, expected near-instant", elapsed)
	}
}

func TestRunHooks_NoHooks(t *testing.T) {
	th := &Thread{}
	ctx := context.Background()
	tc := turnContext{ThreadID: "test", SessionKey: "test-session"}

	result := th.runHooks(ctx, tc)

	if result != nil {
		t.Errorf("expected nil for no hooks, got %v", result)
	}
}
