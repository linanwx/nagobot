package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// SearchProviderStatus holds the health check result for a search provider.
type SearchProviderStatus struct {
	Name      string
	Reachable bool
	AvgMs     int64         // average latency across probe requests
	Error     string        // last error if unreachable
	TestedAt  time.Time
}

// SearchHealthChecker periodically probes all search providers.
type SearchHealthChecker struct {
	providers map[string]SearchProvider

	mu       sync.RWMutex
	statuses map[string]*SearchProviderStatus
}

// NewSearchHealthChecker creates a health checker for the given providers.
func NewSearchHealthChecker(providers map[string]SearchProvider) *SearchHealthChecker {
	return &SearchHealthChecker{
		providers: providers,
		statuses:  make(map[string]*SearchProviderStatus),
	}
}

// Run starts the periodic health check. Call in a goroutine.
// First check after initialDelay, then every interval.
func (h *SearchHealthChecker) Run(ctx context.Context, initialDelay, interval time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	h.checkAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.checkAll(ctx)
		}
	}
}

const probeQuery = "test"

func (h *SearchHealthChecker) checkAll(ctx context.Context) {
	logger.Info("search health check starting")
	for name, p := range h.providers {
		if !p.Available() {
			h.mu.Lock()
			h.statuses[name] = &SearchProviderStatus{
				Name:     name,
				Error:    "not configured",
				TestedAt: time.Now(),
			}
			h.mu.Unlock()
			continue
		}
		status := h.probe(ctx, name, p)
		h.mu.Lock()
		h.statuses[name] = status
		h.mu.Unlock()
		logger.Info("search health check", "source", name, "reachable", status.Reachable, "avgMs", status.AvgMs, "err", status.Error)
	}
}

func (h *SearchHealthChecker) probe(ctx context.Context, name string, p SearchProvider) *SearchProviderStatus {
	const attempts = 2
	var totalMs int64
	var lastErr string

	for i := 0; i < attempts; i++ {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		start := time.Now()
		_, err := p.Search(probeCtx, probeQuery, 1)
		elapsed := time.Since(start).Milliseconds()
		cancel()

		if err != nil {
			lastErr = err.Error()
			continue
		}
		totalMs += elapsed
	}

	if lastErr != "" && totalMs == 0 {
		// Both attempts failed.
		return &SearchProviderStatus{
			Name:     name,
			Error:    lastErr,
			TestedAt: time.Now(),
		}
	}

	successCount := attempts
	if lastErr != "" {
		successCount = 1 // one succeeded, one failed
	}

	return &SearchProviderStatus{
		Name:      name,
		Reachable: true,
		AvgMs:     totalMs / int64(successCount),
		TestedAt:  time.Now(),
	}
}

// Status returns a copy of all provider statuses.
func (h *SearchHealthChecker) Status() map[string]*SearchProviderStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]*SearchProviderStatus, len(h.statuses))
	for k, v := range h.statuses {
		cp := *v
		out[k] = &cp
	}
	return out
}

// StatusSummary returns a compact multi-line summary for YAML headers.
func (h *SearchHealthChecker) StatusSummary() string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.statuses) == 0 {
		return "not yet tested"
	}

	names := make([]string, 0, len(h.statuses))
	for n := range h.statuses {
		names = append(names, n)
	}
	sort.Strings(names)

	var parts []string
	for _, name := range names {
		s := h.statuses[name]
		if s.Reachable {
			parts = append(parts, fmt.Sprintf("%s: ok (%dms, %s)", name, s.AvgMs, formatTimeAgo(s.TestedAt)))
		} else if s.Error == "not configured" {
			parts = append(parts, fmt.Sprintf("%s: not configured", name))
		} else {
			parts = append(parts, fmt.Sprintf("%s: unreachable (%s)", name, formatTimeAgo(s.TestedAt)))
		}
	}
	return strings.Join(parts, "; ")
}

// DetailedStatus returns verbose status for error scenarios.
func (h *SearchHealthChecker) DetailedStatus() string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.statuses) == 0 {
		return "Search provider health check has not run yet."
	}

	names := make([]string, 0, len(h.statuses))
	for n := range h.statuses {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("Search provider status:\n")
	for _, name := range names {
		s := h.statuses[name]
		if s.Reachable {
			sb.WriteString(fmt.Sprintf("  %s: reachable (avg %dms, tested %s)\n", name, s.AvgMs, s.TestedAt.Format("2006-01-02 15:04:05")))
		} else if s.Error == "not configured" {
			sb.WriteString(fmt.Sprintf("  %s: not configured (API key missing)\n", name))
		} else {
			sb.WriteString(fmt.Sprintf("  %s: UNREACHABLE (tested %s, error: %s)\n", name, s.TestedAt.Format("2006-01-02 15:04:05"), s.Error))
		}
	}
	return sb.String()
}

func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
