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
	// progressStreamingQuiet is how long after the last user-visible text delta
	// a turn still counts as "answering". Text alone proves nothing — measured
	// across the fleet, 8.5% of tool-calling assistant messages open with prose
	// (p50 72 runes) and then call a tool — so the signal is not that text
	// appeared but that it is STILL appearing. 10s is far longer than the gap
	// between deltas of a live generation, and short enough that a preamble
	// followed by a slow tool becomes reportable well inside one 30s sweep.
	progressStreamingQuiet = 10 * time.Second
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
	// turnEndReplyCap bounds the child's final reply fed to the summarizer for
	// a turn-end report. The parent reads the full reply from the session
	// file; the summarizer only needs enough to say what came out of it.
	turnEndReplyCap = 2000
)

// progressTurnEndedTag marks a WakeProgress body as a child's end-of-turn
// notice rather than a running snapshot. Compression keys on it: a running
// snapshot ignored via dispatch({}) is noise, while an end-of-turn notice is
// the only record that the work finished and where its result lives.
const progressTurnEndedTag = "event: turn_ended"

// Summary request modes, stated on the first line of the summarizer's wake
// body so the progress-summary agent knows which note to write.
const (
	summaryModeProgress = "Mode: progress"
	summaryModeTurnEnd  = "Mode: turn-ended"
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
// The monitored thread is never touched: no interruption, no injected
// messages. If the observed turn ends before the summary arrives, the note is
// dropped.
//
// It also reports the END of a child's turn (ReportTurnEnd, driven by the
// manager's turn-end hook rather than the sweep). That is how a dispatched
// subagent's work comes back: nothing forwards the child's reply, so the
// session that dispatched it is told the turn ended and reads the result from
// the child's session file itself. The notice is an event, not a verdict: the
// child may have finished, failed, or be waiting on children of its own, and
// the parent checks which.
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

		// Hold off while the answer is visibly arriving. A progress note exists
		// to break a silent wait; delivered on top of a reply the user is
		// already reading it is pure interruption. Suppression is transient —
		// the next sweep reports if the text stopped and the turn ran on — and
		// it fails in the safe direction: a zero stamp (non-streaming provider,
		// or no text yet) suppresses nothing, so behaviour is unchanged
		// wherever we cannot actually tell.
		if !info.LastTextDeltaAt.IsZero() && now.Sub(info.LastTextDeltaAt) < progressStreamingQuiet {
			continue
		}

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
	summary := p.summarize(ctx, key, buildSummaryRequest(info))
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

// summarize runs one progress-summary sibling turn for the session key and
// returns the note ("" on timeout/failure/empty).
func (p *ProgressScanner) summarize(ctx context.Context, sessionKey, request string) string {
	ch := make(chan string, 1)
	key := sessionKey + session.ProgressSummarySessionSuffix
	p.mgr.Wake(key, &WakeMessage{
		Source:    WakeProgressSum,
		Message:   request,
		AgentName: progressSummaryAgent,
		Sinks: NewSinks(SessionSink{
			Label: "progress-summary session: result returns via callback, never delivered to a channel",
			Send:  func(context.Context, string) error { return nil },
		}),
		OnComplete: func(response string) { ch <- response },
	})

	select {
	case result := <-ch:
		return strings.TrimSpace(result)
	case <-time.After(progressSummaryTimeout):
		logger.Warn("progress summary timeout", "session", sessionKey)
		return ""
	case <-ctx.Done():
		return ""
	}
}

// buildSummaryRequest renders the summarizer's wake body for a RUNNING turn:
// the origin request plus the trimmed tool trace.
func buildSummaryRequest(info msg.ThreadInfo) string {
	return summaryModeProgress + "\n\n" + summaryTraceSection(info)
}

// buildTurnEndSummaryRequest renders the summarizer's wake body for a turn
// that has ENDED: the same trace, plus the final reply the turn produced.
func buildTurnEndSummaryRequest(te TurnEnd) string {
	var sb strings.Builder
	sb.WriteString(summaryModeTurnEnd + "\n\n")
	sb.WriteString(summaryTraceSection(te.Info))
	reply := strings.TrimSpace(te.FinalReply)
	if reply == "" {
		reply = "(the turn ended without any reply text)"
	}
	fmt.Fprintf(&sb, "\nThe turn has ENDED. Its final reply (truncated):\n%s\n", truncateStr(reply, turnEndReplyCap))
	return sb.String()
}

// summaryTraceSection is the part both request modes share. Field-level
// trimming already happened at record time (toolTraceFieldRunes); this only
// windows the call count.
func summaryTraceSection(info msg.ThreadInfo) string {
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

// turnEndTarget reports whether a finished turn is a dispatched child's turn
// whose end must be reported, and to whom: the session that dispatched it.
//
// Every such turn is reported, including one that dispatched children of its
// own and is now waiting on them. Deciding "is the child really done" would
// mean reading thread state across a race (a grandchild's notice can be in
// flight while every thread looks idle), and the parent can answer it far
// better by reading the child's reply. So the notice says only that the turn
// ended.
//
// Sources: a session wake (dispatched work, or a peer/parent message), a
// resume of one, and a progress wake (a grandchild's end-of-turn notice, which
// is how a child that was waiting gets to finish).
func turnEndTarget(te TurnEnd) (string, bool) {
	if session.IsInternalSiblingSession(te.SessionKey) {
		return "", false
	}
	parent, ok := session.ImmediateParentKey(te.SessionKey)
	if !ok {
		return "", false
	}
	switch te.Source {
	case WakeSession, WakeResume, WakeProgress:
		return parent, true
	}
	return "", false
}

// ReportTurnEnd is the manager's turn-end hook. For a dispatched child it
// summarizes the finished turn and wakes the session that dispatched it with an
// end-of-turn notice. Non-blocking: the summarizer is an LLM call, and the
// child's thread must not wait on it.
func (p *ProgressScanner) ReportTurnEnd(te TurnEnd) {
	parent, ok := turnEndTarget(te)
	if !ok {
		return
	}
	go p.reportTurnEnd(context.Background(), parent, te)
}

func (p *ProgressScanner) reportTurnEnd(ctx context.Context, parent string, te TurnEnd) {
	report := p.turnEndReport(ctx, te)
	sessionFile := ""
	if cfg := p.mgr.cfg; cfg != nil && cfg.Sessions != nil {
		sessionFile = cfg.Sessions.PathForKey(te.SessionKey)
	}
	p.mgr.Wake(parent, &WakeMessage{
		Source:  WakeProgress,
		Message: buildTurnEndBody(te, sessionFile, report),
		Sender:  "system",
		CallerSink: SessionSink{
			Label: "Caller is progress monitor: reply to caller is dropped",
			Send:  func(context.Context, string) error { return nil },
		},
		Traceparent: te.Traceparent,
	})
	logger.Info("turn-end notice sent", "session", te.SessionKey, "target", parent, "failed", te.Err != nil)
}

// turnEndReport is the human-readable part of the notice. It is never empty:
// the notice is the only way the parent learns the turn ended, so a missing
// summarizer or a failed summary still produces a notice that says so,
// rather than no notice at all.
func (p *ProgressScanner) turnEndReport(ctx context.Context, te TurnEnd) string {
	if te.Err != nil {
		return "The turn ended with an ERROR: " + te.Err.Error()
	}
	if !p.summarizerConfigured() {
		return "(No summary: the progress-summary agent is not configured on this deployment.)"
	}
	if s := p.summarize(ctx, te.SessionKey, buildTurnEndSummaryRequest(te)); s != "" {
		return s
	}
	return "(No summary: the summarizer timed out or returned nothing.)"
}

// buildTurnEndBody renders the end-of-turn notice the dispatching session
// receives.
func buildTurnEndBody(te TurnEnd, sessionFile, report string) string {
	icon, what := "🏁", "ended its turn"
	if te.Err != nil {
		icon, what = "⚠️", "ended its turn with an error"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s subagent %s %s · %s · %d steps\n", icon, te.SessionKey, what,
		humanizeDuration(te.Info.ElapsedSec), te.Info.TotalToolCalls)
	sb.WriteString(progressTurnEndedTag + "\n")
	fmt.Fprintf(&sb, "child_session: %s\n", te.SessionKey)
	if sessionFile != "" {
		fmt.Fprintf(&sb, "session_file: %s\n", sessionFile)
	}
	sb.WriteString("\n" + report + "\n\n")
	sb.WriteString("This notice only says the turn ENDED, not that the task is done or done right. " +
		"The child's actual output is the last role=assistant entry in session_file: read it before acting.")
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
		CallerSink: SessionSink{
			Label: "Caller is progress monitor: reply to caller is dropped",
			Send: func(_ context.Context, response string) error {
				if strings.TrimSpace(response) != "" {
					logger.Debug("progress: caller output dropped", "session", ancestor, "bytes", len(response))
				}
				return nil
			},
		},
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
