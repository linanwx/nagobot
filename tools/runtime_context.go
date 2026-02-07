package tools

import (
	"context"
	"path/filepath"
	"strings"
)

type runtimeContextKey struct{}

// RuntimeContext carries lightweight per-run metadata for tools.
type RuntimeContext struct {
	SessionKey string
	Workspace  string
}

// WithRuntimeContext injects tool runtime metadata into context.
func WithRuntimeContext(ctx context.Context, rt RuntimeContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	rt.SessionKey = strings.TrimSpace(rt.SessionKey)
	rt.Workspace = strings.TrimSpace(rt.Workspace)
	if rt.Workspace != "" {
		if absPath, err := filepath.Abs(rt.Workspace); err == nil {
			rt.Workspace = absPath
		}
	}
	return context.WithValue(ctx, runtimeContextKey{}, rt)
}

// RuntimeContextFrom extracts tool runtime metadata from context.
func RuntimeContextFrom(ctx context.Context) RuntimeContext {
	if ctx == nil {
		return RuntimeContext{}
	}
	rt, _ := ctx.Value(runtimeContextKey{}).(RuntimeContext)
	rt.SessionKey = strings.TrimSpace(rt.SessionKey)
	rt.Workspace = strings.TrimSpace(rt.Workspace)
	if rt.Workspace != "" {
		if absPath, err := filepath.Abs(rt.Workspace); err == nil {
			rt.Workspace = absPath
		}
	}
	return rt
}
