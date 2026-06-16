package thread

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread/msg"
)

const (
	progressScanInterval = 30 * time.Second // how often the scanner sweeps active threads
	progressMinElapsed   = 60               // seconds a child must have run before its first report
	progressInterval     = 60 * time.Second // minimum gap between reports for one child
	progressWindow       = 3                // number of recent tool calls included in a report
	// Safety cap on the report body (rune-safe). Each line shows full
	// ArgsSummary + ResultPreview (≤200 chars each upstream), so 3 lines fit
	// comfortably; this only guards against pathological input.
	progressMaxBytes = 2000
	// progressMaxDispatchNudges caps how many times a single progress turn is
	// re-prompted to use dispatch before the runner gives up and ends it silently
	// (the plain text is dropped regardless). Keeps a non-complying model from
	// burning the whole maxIterations budget on a low-value progress turn.
	progressMaxDispatchNudges = 2
)

// ProgressScanner periodically reports a long-running child session's progress
// to its user-facing ancestor (the "main thread"). The child is never touched:
// the scanner reads the live ExecMetrics snapshot via Manager.ListThreads and
// synthesizes the report mechanically (no LLM, no interruption). The report is
// delivered as a WakeProgress wake so the ancestor's LLM can decide whether to
// surface it to the user, ignore it, or stop the child.
type ProgressScanner struct {
	mgr        *Manager
	lastReport map[string]time.Time // child session key -> last report time
}

// NewProgressScanner creates a scanner bound to the given manager.
func NewProgressScanner(mgr *Manager) *ProgressScanner {
	return &ProgressScanner{mgr: mgr, lastReport: make(map[string]time.Time)}
}

// Run sweeps active threads on a ticker until ctx is cancelled.
func (p *ProgressScanner) Run(ctx context.Context) {
	ticker := time.NewTicker(progressScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.scanOnce()
		}
	}
}

// scanOnce reports every eligible running child once per progressInterval.
func (p *ProgressScanner) scanOnce() {
	now := time.Now()
	threads := p.mgr.ListThreads()
	seen := make(map[string]bool, len(threads))

	for _, info := range threads {
		key := info.SessionKey
		ancestor, ok := progressEligible(info)
		if !ok {
			continue
		}

		seen[key] = true
		if last, had := p.lastReport[key]; had && now.Sub(last) < progressInterval {
			continue
		}

		body := formatProgress(key, info)
		target := ancestor
		dropSink := Sink{
			Label: "Caller is progress monitor — reply to caller is dropped",
			Send: func(_ context.Context, response string) error {
				if strings.TrimSpace(response) != "" {
					logger.Debug("progress: caller output dropped", "session", target, "bytes", len(response))
				}
				return nil
			},
		}
		p.mgr.Wake(target, &WakeMessage{
			Source:  WakeProgress,
			Message: body,
			Sender:  "system",
			Sink:    dropSink,
		})
		p.lastReport[key] = now
		logger.Info("progress report sent",
			"child", key, "ancestor", target,
			"elapsedSec", info.ElapsedSec, "steps", info.TotalToolCalls)
	}

	// Prune state for children that are no longer running (thread finished/GC'd).
	for key := range p.lastReport {
		if !seen[key] {
			delete(p.lastReport, key)
		}
	}
}

// progressEligible reports whether a thread is a running child of a user-facing
// session that has run long enough to report, returning the ancestor key to
// report to. ancestor == info.SessionKey would mean the key is not a child, so
// that case returns ok=false.
func progressEligible(info msg.ThreadInfo) (ancestor string, ok bool) {
	if info.State != "running" || info.ElapsedSec < progressMinElapsed {
		return "", false
	}
	anc, userFacing := userFacingAncestor(info.SessionKey)
	if !userFacing || anc == info.SessionKey {
		return "", false
	}
	return anc, true
}

// formatProgress builds a mechanical, byte-capped progress snapshot for a child.
// The child session key is included verbatim so the ancestor can stop it.
func formatProgress(childKey string, info msg.ThreadInfo) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🔍 subagent %s · running %s · %d steps\n", childKey, humanizeDuration(info.ElapsedSec), info.TotalToolCalls)

	trace := info.ToolTrace
	if n := len(trace); n > progressWindow {
		trace = trace[n-progressWindow:]
	}
	if len(trace) > 0 {
		sb.WriteString("recent:\n")
		for i := range trace {
			rec := trace[i]
			marker := "✓"
			if rec.Error {
				marker = "✗"
			}
			// ArgsSummary / ResultPreview are already capped at 200 chars upstream
			// (in RecordToolCall); show them in full, with newlines flattened so
			// each call stays on one line.
			args := flattenWS(rec.ArgsSummary)
			result := flattenWS(rec.ResultPreview)
			if result != "" {
				fmt.Fprintf(&sb, "- %s(%s) → %s %s\n", rec.Name, args, result, marker)
			} else {
				fmt.Fprintf(&sb, "- %s(%s) %s\n", rec.Name, args, marker)
			}
		}
	}
	if info.CurrentTool != "" {
		fmt.Fprintf(&sb, "current: %s ⏳\n", info.CurrentTool)
	}

	return capBytes(sb.String(), progressMaxBytes)
}

// humanizeDuration renders a second count as "45s" / "6m12s" / "1h03m".
func humanizeDuration(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	m, s := sec/60, sec%60
	if m < 60 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

// flattenWS collapses all runs of whitespace (incl. newlines) to single spaces,
// keeping a multi-line args/result preview on one progress line.
func flattenWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// capBytes hard-caps s to maxBytes without splitting a UTF-8 rune.
func capBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := []byte(s)
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return string(b[:cut]) + "…"
}
