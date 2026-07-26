package thread

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread/msg"
)

const (
	progressScanInterval = 30 * time.Second // how often the scanner sweeps active threads
	progressMinElapsed   = 60               // seconds a turn must have run before its first report
	progressInterval     = 60 * time.Second // minimum gap between reports for one thread
	// progressOriginCap bounds the origin-request runes kept in ExecMetrics and
	// fed to the summarizer (rune-safe via truncateStr).
	progressOriginCap = 2000
	// progressMaxCalls bounds how many recent tool calls feed one summary
	// request. Each record's args/result are already ≤toolTraceFieldRunes (500)
	// upstream, so the request stays ≲45K runes even at the cap.
	progressMaxCalls = 40
	// progressSummaryTimeout bounds one summarizer sibling turn. The summarizer
	// is a small text-only turn on a value model; well past this something is
	// wrong and the report is skipped.
	progressSummaryTimeout = 45 * time.Second
	// progressSummaryAgent is the tools-disabled stateless sibling agent
	// (specialty: [lowcost]) that turns a tool trace into a progress note.
	progressSummaryAgent = "progress-summary"
)

// ProgressScanner periodically reports long-running turns to the person waiting
// on them. Every progressInterval per thread it snapshots the live ExecMetrics
// (origin request + trimmed tool trace) via Manager.ListThreads, asks the
// progress-summary sibling agent (tools disabled, lowcost specialty) for a
// short note, and delivers it:
//
//   - main user-facing session (user-visible turn source): the note goes
//     straight out the thread's defaultSink to the channel user.
//   - subagent/fork child of a user-facing ancestor: the note rides a
//     WakeProgress wake to that ancestor, whose LLM decides whether to surface
//     it (plain reply text) or drop it (dispatch({})).
//
// The monitored thread is never touched — no interruption, no injected
// messages. If the observed turn ends before the summary arrives, the note is
// dropped.
type ProgressScanner struct {
	mgr *Manager

	mu         sync.Mutex
	lastReport map[string]time.Time // monitored session key -> last report kickoff
	inFlight   map[string]bool      // monitored session keys with a summary in progress
}

// NewProgressScanner creates a scanner bound to the given manager.
func NewProgressScanner(mgr *Manager) *ProgressScanner {
	return &ProgressScanner{
		mgr:        mgr,
		lastReport: make(map[string]time.Time),
		inFlight:   make(map[string]bool),
	}
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
			p.scanOnce(ctx)
		}
	}
}

// reportJob is one progress report selected by a scan: the thread snapshot and
// the delivery target (== info.SessionKey for main-session direct-to-user).
type reportJob struct {
	info   msg.ThreadInfo
	target string
}

// scanOnce kicks off one report per eligible running thread, at most once per
// progressInterval per thread. Reports run in goroutines (the summarizer is an
// LLM call) guarded by inFlight so a slow summary never stacks a second one.
func (p *ProgressScanner) scanOnce(ctx context.Context) {
	if !p.summarizerConfigured() {
		return
	}
	for _, job := range p.selectReports(time.Now()) {
		go func(job reportJob) {
			defer func() {
				p.mu.Lock()
				delete(p.inFlight, job.info.SessionKey)
				p.mu.Unlock()
			}()
			p.report(ctx, job.info, job.target)
		}(job)
	}
}

// selectReports returns the reports due this scan and updates throttle state
// (lastReport stamped, inFlight marked — the caller's goroutine must clear it).
// Also prunes throttle state for threads no longer running.
func (p *ProgressScanner) selectReports(now time.Time) []reportJob {
	threads := p.mgr.ListThreads()
	seen := make(map[string]bool, len(threads))
	var jobs []reportJob

	for _, info := range threads {
		key := info.SessionKey
		target, ok := progressEligible(info)
		if !ok {
			continue
		}
		seen[key] = true

		p.mu.Lock()
		last, had := p.lastReport[key]
		busy := p.inFlight[key]
		if busy || (had && now.Sub(last) < progressInterval) {
			p.mu.Unlock()
			continue
		}
		p.lastReport[key] = now
		p.inFlight[key] = true
		p.mu.Unlock()

		jobs = append(jobs, reportJob{info: info, target: target})
	}

	// Prune throttle state for threads that are no longer running.
	p.mu.Lock()
	for key := range p.lastReport {
		if !seen[key] && !p.inFlight[key] {
			delete(p.lastReport, key)
		}
	}
	p.mu.Unlock()
	return jobs
}

// summarizerConfigured reports whether the progress-summary agent template is
// loaded. Without it (workspace not synced yet) the scanner does nothing.
func (p *ProgressScanner) summarizerConfigured() bool {
	cfg := p.mgr.cfg
	return cfg != nil && cfg.Agents != nil && cfg.Agents.Def(progressSummaryAgent) != nil
}

// progressEligible reports whether a running thread should be progress-reported,
// returning the delivery target session key (== info.SessionKey for a main
// session reporting straight to its user; a user-facing ancestor for a child).
//
// Gating beyond "running long enough with tool activity":
//   - internal helper siblings (prethink / previews / progress-summary itself)
//     are never reported — recursion guard.
//   - a main session reports only turns woken by a real user message. Heartbeat,
//     cron, compression, and cross-session turns on a user-facing key must never
//     message the user.
//   - a child reports only delegated-work turns (session wake from its parent,
//     or a resume of one).
func progressEligible(info msg.ThreadInfo) (target string, ok bool) {
	if info.State != "running" || info.ElapsedSec < progressMinElapsed || info.TotalToolCalls == 0 {
		return "", false
	}
	if session.IsInternalSiblingSession(info.SessionKey) {
		return "", false
	}
	anc, userFacing := userFacingAncestor(info.SessionKey)
	if !userFacing {
		return "", false
	}
	src := msg.WakeSource(info.TurnWakeSource)
	if anc == info.SessionKey {
		if !msg.IsUserVisibleSource(src) {
			return "", false
		}
	} else if src != msg.WakeSession && src != msg.WakeResume {
		return "", false
	}
	return anc, true
}

// report summarizes one running turn via the progress-summary sibling and
// delivers the note. Blocking (called in its own goroutine).
func (p *ProgressScanner) report(ctx context.Context, info msg.ThreadInfo, target string) {
	key := info.SessionKey
	summary := p.summarize(ctx, info)
	if summary == "" {
		return
	}
	// Drop the note if the observed turn already ended — a progress note
	// arriving after the final answer reads as noise (for a child, the parent
	// gets the real completion wake anyway).
	th := p.mgr.runningTurnThread(key, info.TurnStart)
	if th == nil {
		logger.Info("progress note dropped, turn ended", "session", key)
		return
	}

	if target == key {
		p.deliverToUser(ctx, th, summary)
	} else {
		p.deliverToAncestor(key, target, info, summary)
	}
	logger.Info("progress report sent",
		"session", key, "target", target,
		"elapsedSec", info.ElapsedSec, "steps", info.TotalToolCalls)
}

// summarize runs one progress-summary sibling turn and returns the note ("" on
// timeout/failure/empty).
func (p *ProgressScanner) summarize(ctx context.Context, info msg.ThreadInfo) string {
	ch := make(chan string, 1)
	key := info.SessionKey + session.ProgressSummarySessionSuffix
	p.mgr.Wake(key, &WakeMessage{
		Source:    WakeProgressSum,
		Message:   buildSummaryRequest(info),
		AgentName: progressSummaryAgent,
		Sinks: NewSinks(SessionSink{
			Label: "progress-summary session — result returns via callback, never delivered to a channel",
			Send:  func(context.Context, string) error { return nil },
		}),
		OnComplete: func(response string) { ch <- response },
	})

	select {
	case result := <-ch:
		return strings.TrimSpace(result)
	case <-time.After(progressSummaryTimeout):
		logger.Warn("progress summary timeout", "session", info.SessionKey)
		return ""
	case <-ctx.Done():
		return ""
	}
}

// buildSummaryRequest renders the summarizer's wake body: the origin request
// plus the trimmed tool trace. Field-level trimming already happened at record
// time (toolTraceFieldRunes); this only windows the call count.
func buildSummaryRequest(info msg.ThreadInfo) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Original request (the turn below is working on this):\n%s\n\n", info.OriginRequest)

	trace := info.ToolTrace
	dropped := 0
	if n := len(trace); n > progressMaxCalls {
		dropped = n - progressMaxCalls
		trace = trace[dropped:]
	}
	fmt.Fprintf(&sb, "Tool activity so far (%d calls total", info.TotalToolCalls)
	if dropped > 0 {
		fmt.Fprintf(&sb, "; oldest %d omitted", dropped)
	}
	sb.WriteString("; oldest first; args and results truncated):\n")
	for i := range trace {
		rec := trace[i]
		marker := ""
		if rec.Error {
			marker = " [FAILED]"
		}
		fmt.Fprintf(&sb, "- %s(%s)%s\n", rec.Name, rec.ArgsSummary, marker)
		if rec.ResultPreview != "" {
			fmt.Fprintf(&sb, "  → %s\n", rec.ResultPreview)
		}
	}
	if info.CurrentTool != "" {
		fmt.Fprintf(&sb, "\nCurrently executing: %s\n", info.CurrentTool)
	}
	fmt.Fprintf(&sb, "\nElapsed: %s\n", humanizeDuration(info.ElapsedSec))
	return sb.String()
}

// deliverToUser sends a main session's progress note straight out its
// defaultSink (the channel user), bypassing the busy thread. Mirrors
// recordProactiveChat's chat.jsonl bookkeeping so pre-think's recent-chat
// context sees the note.
func (p *ProgressScanner) deliverToUser(ctx context.Context, th *Thread, summary string) {
	th.mu.Lock()
	key := th.sessionKey
	sink := th.defaultSink
	th.mu.Unlock()
	if sink.IsZero() {
		logger.Warn("progress note undeliverable, no default sink", "session", key)
		return
	}
	if err := sink.Send(ctx, summary); err != nil {
		logger.Warn("progress note delivery failed", "session", key, "err", err)
		return
	}
	if dir := p.mgr.SessionDir(key); dir != "" {
		if err := session.AppendChat(dir, session.ChatRoleAssistant, "progress", summary, time.Now()); err != nil {
			logger.Warn("chat.jsonl progress write failed", "session", key, "err", err)
		}
	}
}

// deliverToAncestor wakes the user-facing ancestor with the child's summary as
// a WakeProgress wake. The ancestor's LLM decides whether to surface it
// (plain reply text) or drop it (dispatch({})); its reply-to-caller is dropped.
func (p *ProgressScanner) deliverToAncestor(childKey, ancestor string, info msg.ThreadInfo, summary string) {
	body := fmt.Sprintf("🔍 subagent %s · running %s · %d steps\n\n%s",
		childKey, humanizeDuration(info.ElapsedSec), info.TotalToolCalls, summary)
	p.mgr.Wake(ancestor, &WakeMessage{
		Source:  WakeProgress,
		Message: body,
		Sender:  "system",
		Sinks: NewSinks(SessionSink{
			Label: "Caller is progress monitor — reply to caller is dropped",
			Send: func(_ context.Context, response string) error {
				if strings.TrimSpace(response) != "" {
					logger.Debug("progress: caller output dropped", "session", ancestor, "bytes", len(response))
				}
				return nil
			},
		}),
	})
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
