package thread

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/tools"
)

// run executes one thread turn. Called by RunOnce; callers must not invoke
// this directly.
func (t *Thread) run(ctx context.Context, userMessage string, media []string, sink Sink, callerKey string, injectFn func() []provider.Message, wakeSource string) (string, error) {
	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" {
		return "", nil
	}

	cfg := t.cfg()
	systemPrompt := t.buildSystemPrompt()

	// Stateless agents (tier_lossy_mode: stateless) begin every turn with a
	// blank session: clear it before loading so the turn sees no prior history
	// and nothing accumulates across a continuous conversation. The turn's own
	// write-ahead + persistence then leave exactly this one turn on disk.
	t.clearIfStateless(cfg)

	sess := t.loadSession()
	messages, turnUserMessages := t.buildMessageHistory(ctx, systemPrompt, userMessage, media, sess)

	// Write-ahead: persist user messages before LLM call so they survive a crash.
	if sess != nil {
		if wakeSource != "" {
			for i := range turnUserMessages {
				turnUserMessages[i].Source = wakeSource
			}
		}
		if err := cfg.Sessions.Append(t.sessionKey, turnUserMessages...); err != nil {
			logger.Warn("write-ahead save failed", "key", t.sessionKey, "err", err)
		}
	}

	// Set up execution metrics for observability by other threads.
	metrics := &ExecMetrics{TurnStart: time.Now()}
	metrics.Media = CollectMediaBreakdown(messages)
	t.mu.Lock()
	t.execMetrics = metrics
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.execMetrics = nil
		t.mu.Unlock()
	}()

	runCtx := tools.WithRuntimeContext(ctx, tools.RuntimeContext{
		SessionKey:            t.sessionKey,
		Workspace:             cfg.Workspace,
		SessionDir:            t.mgr.SessionDir(t.sessionKey),
		SupportsVision:        t.currentModelSupportsVision(),
		SupportsAudio:         t.currentModelSupportsAudio(),
		SupportsPDF:           t.currentModelSupportsPDF(),
		ImageReaderConfigured: cfg.Agents != nil && cfg.Agents.Def("imagereader") != nil,
		AudioReaderConfigured: cfg.Agents != nil && cfg.Agents.Def("audioreader") != nil,
		PDFReaderConfigured:   cfg.Agents != nil && cfg.Agents.Def("pdfreader") != nil,
	})
	t.resetHaltLoop()
	t.mu.Lock()
	t.currentSink = sink
	t.currentCallerKey = callerKey
	t.progressDispatchNudges = 0
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.currentSink = Sink{}
		t.currentCallerKey = ""
		t.mu.Unlock()
	}()
	p := t.resolveProvider()
	if p == nil {
		return noProviderMessage(), nil
	}

	// Incremental persistence: save each message as it arrives during the agentic loop.
	var persistMsg func(m provider.Message)
	if sess != nil {
		persistMsg = func(m provider.Message) {
			if wakeSource != "" {
				m.Source = wakeSource
			}
			if err := cfg.Sessions.Append(t.sessionKey, m); err != nil {
				logger.Warn("incremental save failed", "key", t.sessionKey, "err", err)
			}
		}
	}

	response, _, usage, _, providerLabel, modelLabel, err := t.executeRunner(ctx, runCtx, p, metrics, messages, sink, injectFn, persistMsg)
	if err != nil {
		t.recordTurn(metrics, "", "", "", wakeSource, usage, true)
		return "", err
	}
	providerName, modelName := providerLabel, modelLabel
	if providerName == "" || modelName == "" {
		providerName, modelName = t.resolvedProviderModel()
	}
	agentName := ""
	t.mu.Lock()
	if t.Agent != nil {
		agentName = t.Agent.Name
	}
	t.mu.Unlock()
	t.recordTurn(metrics, providerName, modelName, agentName, wakeSource, usage, false)
	return response, nil
}

// clearIfStateless wipes the thread's session to empty when the active agent is
// configured with tier_lossy_mode: stateless, so the agent begins each turn with
// no history. No-op for every other agent. Called at the start of each turn,
// before the session is loaded — this is what makes a stateless agent truly
// per-turn stateless (the idle-driven slide_window path can't do that).
func (t *Thread) clearIfStateless(cfg *ThreadConfig) {
	if cfg == nil || cfg.Agents == nil || cfg.Sessions == nil {
		return
	}
	t.mu.Lock()
	a := t.Agent
	t.mu.Unlock()
	if a == nil {
		return
	}
	def := cfg.Agents.Def(a.Name)
	if def == nil || def.TierLossyMode != "stateless" {
		return
	}
	if err := cfg.Sessions.Save(&session.Session{Key: t.sessionKey}); err != nil {
		logger.Warn("stateless agent: clear session failed", "key", t.sessionKey, "agent", a.Name, "err", err)
	}
}

// buildSystemPrompt assembles the system prompt from the active agent.
func (t *Thread) buildSystemPrompt() string {
	t.mu.Lock()
	activeAgent := t.Agent
	t.mu.Unlock()

	if activeAgent == nil {
		return "You are a helpful AI assistant."
	}

	skillsSection := t.buildSkillsSection()
	activeAgent.SetLocation(t.location())
	activeAgent.SetSections(t.cfg().Sections)
	activeAgent.Set("TOOLS", t.toolsForTurn().Names())
	activeAgent.Set("SKILLS", skillsSection)
	activeAgent.Set(agent.SectionUserMemory, t.buildUserSection())
	activeAgent.Set(agent.SectionHeartbeatPrompt, t.buildHeartbeatSection())
	activeAgent.Set(agent.SectionMemoryIndex, t.buildMemoryIndexSection())
	activeAgent.Set(agent.SectionDream, t.buildDreamSection())
	activeAgent.Set(agent.SectionFileTrack, t.buildFileTrackSection())
	prompt := activeAgent.Build()
	if strings.TrimSpace(prompt) == "" {
		return "You are a helpful AI assistant."
	}
	return prompt
}

// buildMessageHistory assembles the full message list for the LLM request,
// including system prompt, session history, user message, and hook injections.
// Returns the full messages slice and the turn-specific user messages (for write-ahead).
func (t *Thread) buildMessageHistory(ctx context.Context, systemPrompt, userMessage string, media []string, sess *session.Session) ([]provider.Message, []provider.Message) {
	messages := make([]provider.Message, 0, 2)
	messages = append(messages, provider.SystemMessage(systemPrompt))

	ct := t.contextBudget()
	contextWindowTokens := ct.ContextWindow
	toolDefsTokens := EstimateToolDefsTokens(t.toolsForTurn().Defs())
	maxCompletionTokens := t.cfg().MaxCompletionTokens

	if sess != nil {
		sessionMessages := ApplyCompressed(provider.SanitizeMessages(sess.Messages))
		messages = append(messages, sessionMessages...)
	}

	turnUserMessages := make([]provider.Message, 0, 4)
	userMsg := provider.UserMessage(userMessage)
	if kept := t.keepSupportedMedia(media); len(kept) > 0 {
		userMsg.Media = kept
	}
	messages = append(messages, userMsg)
	turnUserMessages = append(turnUserMessages, userMsg)

	// Bound the request to the shared context budget: drop the oldest
	// assistant+tool_call groups, preserving the system prompt and all user
	// messages (including the first task message). Same logic and budget as the
	// in-loop guard; idempotent and ephemeral (the session file is untouched).
	messages = trimMessageGroups(messages, toolDefsTokens, contextLoopBudget(contextWindowTokens, maxCompletionTokens))

	requestEstimatedTokens := EstimateMessagesTokens(messages) + toolDefsTokens
	logger.Debug(
		"context estimate",
		"threadID", t.id,
		"sessionKey", t.sessionKey,
		"requestEstimatedTokens", requestEstimatedTokens,
		"contextWindowTokens", contextWindowTokens,
		"warnToken", ct.WarnToken,
	)

	sessionPath, _ := t.sessionFilePath() // ok ignored: empty path is acceptable for hooks
	hookInjections := t.runHooks(ctx, turnContext{
		ThreadID:               t.id,
		SessionKey:             t.sessionKey,
		SessionPath:            sessionPath,
		UserMessage:            userMessage,
		RequestEstimatedTokens: requestEstimatedTokens,
		ContextWindowTokens:    contextWindowTokens,
		WarnToken:              ct.WarnToken,
	})
	for _, injection := range hookInjections {
		trimmed := strings.TrimSpace(injection)
		if trimmed == "" {
			continue
		}
		msg := provider.UserMessage(trimmed)
		messages = append(messages, msg)
		turnUserMessages = append(turnUserMessages, msg)
	}

	return messages, turnUserMessages
}

// executeRunner runs the agentic loop with streaming and message callbacks.
func (t *Thread) executeRunner(ctx, runCtx context.Context, p provider.Provider, metrics *ExecMetrics, messages []provider.Message, sink Sink, injectFn func() []provider.Message, persistMsg func(provider.Message)) (response string, intermediates []provider.Message, usage provider.Usage, quota *provider.Quota, providerLabel string, modelLabel string, err error) {
	contextWindowTokens := t.contextBudget().ContextWindow
	maxCompletionTokens := t.cfg().MaxCompletionTokens
	loopBudget := contextLoopBudget(contextWindowTokens, maxCompletionTokens)
	runner := NewRunner(p, t.toolsForTurn(), metrics, loopBudget)
	runner.ShouldHalt(t.isHaltLoop)
	runner.SetUserVisible(sysmsg.IsUserVisibleSource(t.lastWakeSource))

	// Persist per-call estimation accuracy ratios into the session's meta.json.
	if cfg := t.cfg(); cfg.Sessions != nil && t.sessionKey != "" {
		sessionDir := filepath.Dir(cfg.Sessions.PathForKey(t.sessionKey))
		runner.OnEstimationSample(func(providerName, modelName string, ratio float64) {
			session.AppendTokenRatioSample(sessionDir, providerName, modelName, ratio)
		})
	}

	// Reaction: connect lifecycle events to sink reaction.
	if !sink.React.IsZero() && !t.IsHeartbeatWake() {
		runner.OnEvent(func(event RunnerEvent, _ string) {
			if t.isSinkSuppressed() {
				return
			}
			switch event {
			case EventToolCalls:
				sink.React.Do(ctx, ReactToolCalls)
			case EventStreaming:
				sink.React.Do(ctx, ReactStreaming)
			}
		})
	}

	// Streaming: register OnStream for chunkable sinks on non-heartbeat turns.
	var streamer *MarkdownStreamer
	useStreaming := !t.IsHeartbeatWake() && !sink.IsZero() && sink.Chunkable
	if useStreaming {
		streamer = NewMarkdownStreamer(sink, ctx, streamFlushThreshold)
		runner.OnStream(func(streamID, delta string) {
			if ctx.Err() != nil || t.isSinkSuppressed() {
				return
			}
			if delta == "" {
				streamer.Flush() // end-of-stream signal: flush remaining buffer
				return
			}
			streamer.OnDelta(delta)
		})
	}

	// OnMessage: persistence + suppression + delivery for every message.
	runner.OnMessage(func(m provider.Message) {
		// 1. Persist all messages.
		intermediates = append(intermediates, m)
		if persistMsg != nil {
			persistMsg(m)
		}

		if m.Role != "assistant" {
			return
		}

		// 2. Delivery (non-streaming path).
		if sink.IsZero() || t.isSinkSuppressed() || !isUserFacingContent(m.Content) {
			return
		}
		if streamer != nil && streamer.DidSend() {
			return // streaming already delivered this content
		}
		if len(m.ToolCalls) > 0 {
			// Intermediate: deliver for chunkable sinks only.
			if sink.Chunkable {
				if err := sink.Send(ctx, m.Content); err != nil {
					logger.Warn("intermediate delivery failed", "key", t.sessionKey, "sink", sink.Label, "err", err)
				}
			}
		} else {
			// Final response: deliver with retry.
			if err := sink.WithRetry(3).Send(ctx, m.Content); err != nil {
				logger.Warn("final delivery failed", "key", t.sessionKey, "sink", sink.Label, "err", err)
			}
		}
	})

	runner.OnIterationEnd(injectFn)

	// OnNoToolCalls: enforce explicit dispatch on wake sources where naive
	// text routing is ambiguous (currently WakeSession — covers peer-asked
	// and child-completed). The hook suppresses the about-to-fire sink
	// delivery and returns a system reminder; the runner persists the
	// rejected text, appends the reminder, and iterates again until the
	// model emits dispatch (or maxIterations aborts).
	runner.OnNoToolCalls(func(_ string) []provider.Message {
		t.mu.Lock()
		src := t.lastWakeSource
		peerKey := t.currentCallerKey
		t.mu.Unlock()

		// WakeProgress: a progress turn must terminate via dispatch — dispatch(to=user)
		// to surface a note, or dispatch({}) to end silently. Plain text is dropped by
		// the progress monitor's sink (delivers nothing), so reject it: suppress the
		// (dropped) delivery and re-iterate with a corrective reminder, capped at
		// progressMaxDispatchNudges so a non-complying model can't burn the whole
		// maxIterations budget on a low-value progress turn.
		if src == WakeProgress {
			t.SetSuppressSink()
			t.mu.Lock()
			n := t.progressDispatchNudges
			t.progressDispatchNudges++
			t.mu.Unlock()
			if n >= progressMaxDispatchNudges {
				return nil // give up: end the turn silently (text was dropped anyway)
			}
			return []provider.Message{{
				Role:    "user",
				Content: buildProgressDispatchRequiredPayload(time.Now().In(t.location())),
				Source:  sourceProgressDispatchRequired,
			}}
		}

		if !src.RequiresExplicitDispatch() {
			return nil
		}
		if peerKey == "" {
			return nil
		}
		t.SetSuppressSink()
		payload := buildCrossThreadDispatchRequiredPayload(peerKey, time.Now().In(t.location()))
		return []provider.Message{{
			Role:    "user",
			Content: payload,
			Source:  sourceCrossThreadDispatchRequired,
		}}
	})
	runCtx = provider.WithSessionKey(runCtx, t.sessionKey)
	response, err = runner.RunWithMessages(runCtx, messages)
	usage = runner.TotalUsage()
	providerLabel = runner.ProviderLabel()
	modelLabel = runner.ModelLabel()
	if err != nil {
		return "", nil, usage, nil, "", "", err
	}

	return response, intermediates, usage, runner.LastQuota(), providerLabel, modelLabel, nil
}

// parentSessionKey strips known sibling/fork markers from a session key.
// Returns the input unchanged if no marker matches.
func parentSessionKey(key string) string {
	if strings.HasSuffix(key, session.PreThinkSessionSuffix) {
		return strings.TrimSuffix(key, session.PreThinkSessionSuffix)
	}
	if strings.HasSuffix(key, session.AudioPreviewSessionSuffix) {
		return strings.TrimSuffix(key, session.AudioPreviewSessionSuffix)
	}
	if strings.HasSuffix(key, session.ImagePreviewSessionSuffix) {
		return strings.TrimSuffix(key, session.ImagePreviewSessionSuffix)
	}
	if idx := strings.Index(key, session.ForkSessionInfix); idx > 0 {
		return key[:idx]
	}
	// Subagent children ({parent}:threads:{taskID}, e.g. the delegated search
	// agent) inherit the root session's USER.md / memory/ — strip to the part
	// before the first :threads: so their memory sections resolve to the main
	// session, the same way :prethink does.
	if idx := strings.Index(key, session.ThreadsSessionInfix); idx > 0 {
		return key[:idx]
	}
	return key
}

// sectionDir returns the directory to look up a per-session section entry
// (e.g. "USER.md", "memory", "heartbeat.md"). Tries own session dir first;
// if the entry is missing there, falls back to the parent session dir computed
// by stripping known sibling markers. Sibling/fork sessions thus inherit the
// parent's USER.md and memory/ without copying.
func (t *Thread) sectionDir(entry string) string {
	sessionPath, ok := t.sessionFilePath()
	if !ok {
		return ""
	}
	ownDir := filepath.Dir(sessionPath)
	if _, err := os.Stat(filepath.Join(ownDir, entry)); err == nil {
		return ownDir
	}
	parentKey := parentSessionKey(t.sessionKey)
	if parentKey == "" || parentKey == t.sessionKey {
		return ownDir
	}
	cfg := t.cfg()
	if cfg.Sessions == nil {
		return ownDir
	}
	parentDir := filepath.Dir(cfg.Sessions.PathForKey(parentKey))
	if _, err := os.Stat(filepath.Join(parentDir, entry)); err == nil {
		return parentDir
	}
	return ownDir
}

// buildUserSection resolves the per-session USER.md into a YAML-frontmattered section.
func (t *Thread) buildUserSection() string {
	dir := t.sectionDir("USER.md")
	if dir == "" {
		return ""
	}
	userPath := filepath.Join(dir, "USER.md")
	absPath, _ := filepath.Abs(userPath)

	content, err := os.ReadFile(userPath)
	if err != nil {
		return fmt.Sprintf("---\ntype: user_preference\nfile_path: %s\nprompt: Append to store.\n---", absPath)
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return fmt.Sprintf("---\ntype: user_preference\nfile_path: %s\nprompt: Append to store.\n---", absPath)
	}
	prompt := "Append to store."
	lineCount := strings.Count(text, "\n") + 1
	if lineCount > 200 {
		prompt += " WARNING: this file exceeds 200 lines. On next update, remove outdated entries or consolidate existing content to keep it concise."
	}
	return fmt.Sprintf("---\ntype: user_preference\nfile_path: %s\nprompt: %s\n---\n\n%s", absPath, prompt, text)
}

// buildHeartbeatSection resolves the per-session heartbeat.md into a YAML-frontmattered section.
func (t *Thread) buildHeartbeatSection() string {
	dir := t.sectionDir("heartbeat.md")
	if dir == "" {
		return ""
	}
	hbPath := filepath.Join(dir, "heartbeat.md")
	absPath, _ := filepath.Abs(hbPath)

	var body string
	if data, err := os.ReadFile(hbPath); err == nil {
		body = strings.TrimSpace(string(data))
	}

	header := fmt.Sprintf("---\ntype: heartbeat_information\nfile_path: %s\nprompt: Heartbeat automatically wakes the thread to reflect on follow-up items and proactively help users with tasks. Use `use_skill(heartbeat-wake)` to handle heartbeat pulses — it covers both reflection and action.\n---", absPath)
	if body == "" {
		return header
	}
	return header + "\n\n" + body
}

// buildDreamSection resolves the per-session dream.md into a YAML-frontmattered section.
// Returns "" when no dream.md exists, so sessions that have never dreamed add nothing
// to the prompt. The dream skill overwrites dream.md each night; the soul agent
// consumes it here to let the latest dream quietly inform conversations.
func (t *Thread) buildDreamSection() string {
	dir := t.sectionDir("dream.md")
	if dir == "" {
		return ""
	}
	dreamPath := filepath.Join(dir, "dream.md")
	data, err := os.ReadFile(dreamPath)
	if err != nil {
		return ""
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return ""
	}
	absPath, _ := filepath.Abs(dreamPath)
	header := fmt.Sprintf("---\ntype: dream_reflection\nfile_path: %s\nprompt: Your most recent dream — a background reflection over the past day's conversation, rewritten each night by the dream skill. Let it quietly inform how you understand and respond to the user.\n---", absPath)
	return header + "\n\n" + body
}

// buildFileTrackSection resolves the per-session file-track.md into a
// YAML-frontmattered section. Returns "" when no file-track.md exists. The
// file-track skill writes this catalog of the session's workspace files (what
// each file is and when to use it); the agent consumes it here so it knows what
// it already has on disk.
func (t *Thread) buildFileTrackSection() string {
	dir := t.sectionDir("file-track.md")
	if dir == "" {
		return ""
	}
	ftPath := filepath.Join(dir, "file-track.md")
	data, err := os.ReadFile(ftPath)
	if err != nil {
		return ""
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return ""
	}
	absPath, _ := filepath.Abs(ftPath)
	header := fmt.Sprintf("---\ntype: file_track\nfile_path: %s\nprompt: Catalog of the files in this session's workspace — what each file is and when to use it. Run use_skill(\"file-track\") to re-organize and refresh it after you create, change, or finish with files.\n---", absPath)
	return header + "\n\n" + body
}

// buildMemoryIndexSection builds a per-session memory recall index from summarized memory files.
func (t *Thread) buildMemoryIndexSection() string {
	dir := t.sectionDir("memory")
	if dir == "" {
		return ""
	}
	memoryDir := filepath.Join(dir, "memory")
	absMemoryDir, _ := filepath.Abs(memoryDir)

	// Modtime guard: skip re-scan if directory hasn't changed since last build.
	info, err := os.Stat(memoryDir)
	if err != nil {
		return ""
	}
	t.mu.Lock()
	if t.memoryIndexCache != "" && !info.ModTime().After(t.memoryIndexModTime) {
		cached := t.memoryIndexCache
		t.mu.Unlock()
		return cached
	}
	t.mu.Unlock()

	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return ""
	}

	var items []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		f, err := os.Open(filepath.Join(memoryDir, e.Name()))
		if err != nil {
			continue
		}
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		f.Close()
		if n == 0 {
			continue
		}
		yamlBlock, _, ok := SplitFrontmatter(string(buf[:n]))
		if !ok {
			continue
		}
		summary := ExtractFrontmatterValue(yamlBlock, "summary")
		if summary == "" {
			continue
		}
		items = append(items, fmt.Sprintf("- %s: %s", filepath.Join(absMemoryDir, e.Name()), summary))
	}

	// Keep only the most recent 20 entries (filenames are YYYY-MM-DD.md, sorted ascending by ReadDir).
	if len(items) > 20 {
		items = items[len(items)-20:]
	}

	var result string
	if len(items) > 0 {
		header := fmt.Sprintf("---\ntype: memory_index\nfile_path: %s\nprompt: Summaries of past compressed conversations. Use read_file to recall details.\n---", absMemoryDir)
		result = header + "\n\n" + strings.Join(items, "\n")
	}

	t.mu.Lock()
	t.memoryIndexCache = result
	t.memoryIndexModTime = info.ModTime()
	t.mu.Unlock()

	return result
}

// isUserFacingContent returns true if the content is meaningful for the user,
// filtering out known provider-injected placeholders.
func isUserFacingContent(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch s {
	case "(tool call)", "(empty assistant message)":
		return false
	}
	return true
}

// ApplyCompressed returns a copy of messages with compression applied.
// - HeartbeatTrim: assistant/tool messages removed entirely; user msg passes through Compressed→Content.
// - Compressed field: Content replaced with Compressed value.
// - ReasoningTrimmed: reasoning fields cleared.
// The original session data is not modified.
func ApplyCompressed(msgs []provider.Message) []provider.Message {
	result := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.HeartbeatTrim {
			continue
		}
		if m.Compressed != "" {
			m.Content = m.Compressed
		}
		if m.ReasoningTrimmed {
			m.ReasoningContent = ""
			m.ReasoningDetails = provider.StripReasoningKeepSignatures(m.ReasoningDetails)
		}
		result = append(result, m)
	}
	return result
}

// ApplyCompressedMessage applies compression to a single message (content + reasoning).
func ApplyCompressedMessage(m provider.Message) provider.Message {
	if m.Compressed != "" {
		m.Content = m.Compressed
	}
	if m.ReasoningTrimmed {
		m.ReasoningContent = ""
		m.ReasoningDetails = provider.StripReasoningKeepSignatures(m.ReasoningDetails)
	}
	return m
}

// resolvedModelConfig returns the provider/model for the current turn by walking
// the typed routing rules in fixed precedence:
// session > source-specialty > agent > specialty.
// Source-specialty applies only when the turn's wake source matches a key in the
// agent's source_specialty frontmatter; it cascades (an unconfigured entry falls
// through to the agent rule, then the basic specialties, then the default).
// Returns nil when no rule matches (caller falls back to the default model).
// Uses ModelsFn for hot-reload if available, falling back to the startup snapshot.
func (t *Thread) resolvedModelConfig() *config.ModelConfig {
	cfg := t.cfg()

	// 0. Per-wake model override (dispatch subagent/fork). Highest precedence —
	// a deliberate, scoped pin for this wake only (validated at dispatch time
	// against provider key + whitelist). Bypasses all normal session/agent/
	// specialty routing. Both fields are set together; guard on both.
	if p, m := strings.TrimSpace(t.modelOverrideProvider), strings.TrimSpace(t.modelOverrideModel); p != "" && m != "" {
		return &config.ModelConfig{Provider: p, ModelType: m}
	}

	rules := cfg.Models
	if cfg.ModelsFn != nil {
		rules = cfg.ModelsFn()
	}
	if len(rules) == 0 {
		return nil
	}

	// 1. session: pinned by exact session key (highest priority).
	if key := strings.TrimSpace(t.sessionKey); key != "" {
		if r := config.FindModelRule(rules, config.ModelRuleSession, key); r != nil {
			return &config.ModelConfig{Provider: r.Provider, ModelType: r.ModelType}
		}
	}

	if t.Agent == nil || cfg.Agents == nil {
		return nil
	}
	def := cfg.Agents.Def(t.Agent.Name)
	if def == nil {
		return nil
	}

	// 2. source specialty: if this turn's wake source has a specialty list
	// declared in the agent frontmatter (source_specialty), try those
	// left-to-right before the agent rule. Cascades: a declared-but-unconfigured
	// specialty simply falls through to the next step.
	if src := string(t.lastWakeSource); src != "" && len(def.SourceSpecialties) > 0 {
		for _, sp := range def.SourceSpecialties[src] {
			if sp == "" {
				continue
			}
			if r := config.FindModelRule(rules, config.ModelRuleSpecialty, sp); r != nil {
				return &config.ModelConfig{Provider: r.Provider, ModelType: r.ModelType}
			}
		}
	}

	// 3. agent: pinned by agent name.
	if r := config.FindModelRule(rules, config.ModelRuleAgent, def.Name); r != nil {
		return &config.ModelConfig{Provider: r.Provider, ModelType: r.ModelType}
	}

	// 4. specialty: agent's specialties left-to-right, first match wins.
	for _, sp := range def.Specialties {
		if sp == "" {
			continue
		}
		if r := config.FindModelRule(rules, config.ModelRuleSpecialty, sp); r != nil {
			return &config.ModelConfig{Provider: r.Provider, ModelType: r.ModelType}
		}
	}

	return nil
}

func noProviderMessage() string {
	return `No LLM provider configured. To get started, send:

/init --provider openrouter --model moonshotai/kimi-k2.5 --api-key YOUR_KEY

Supported providers: openrouter, anthropic, deepseek, openai`
}

// resolvedProviderModel returns the provider and model name for the current agent.
func (t *Thread) resolvedProviderModel() (string, string) {
	cfg := t.cfg()
	if mc := t.resolvedModelConfig(); mc != nil {
		return mc.Provider, mc.ModelType
	}
	// Prefer the hot-reload view of the default so a config.yaml edit to
	// thread.provider/modelType is reflected without a restart (mirrors
	// ModelsFn). cfg.ProviderName/ModelName is a startup snapshot fallback.
	if cfg.DefaultModelFn != nil {
		if pn, mn := cfg.DefaultModelFn(); pn != "" {
			return pn, mn
		}
	}
	return cfg.ProviderName, cfg.ModelName
}

// recordTurn writes a TurnRecord to the metrics store if available.
func (t *Thread) recordTurn(metrics *ExecMetrics, providerName, modelName, agentName, source string, usage provider.Usage, isError bool) {
	cfg := t.cfg()
	if cfg.MetricsStore == nil || metrics == nil {
		return
	}
	cfg.MetricsStore.Record(monitor.TurnRecord{
		Timestamp:  metrics.TurnStart,
		DurationMs: time.Since(metrics.TurnStart).Milliseconds(),
		Provider:   providerName,
		Model:      modelName,
		Agent:      agentName,
		SessionKey: t.sessionKey,
		Source:     source,
		Iterations: metrics.Iterations,
		ToolCalls:  metrics.TotalToolCalls,
		Error:      isError,

		LastPromptTokens:     metrics.LastPromptActual,
		LastCompletionTokens: metrics.LastCompletionActual,
		LastTotalTokens:      metrics.LastTotalActual,
		LastCachedTokens:     metrics.LastCachedActual,
		LastReasoningTokens:  metrics.LastReasoningActual,

		AccPromptTokens:     usage.PromptTokens,
		AccCompletionTokens: usage.CompletionTokens,
		AccTotalTokens:      usage.TotalTokens,
		AccCachedTokens:     usage.CachedTokens,
		AccReasoningTokens:  usage.ReasoningTokens,

		EstPromptTokens:     metrics.PromptEstimated,
		EstReasoningTokens:  metrics.ReasoningEstimated,
		EstMediaImageCount:  metrics.Media.ImageCount,
		EstMediaImageTokens: metrics.Media.ImageEst,
		EstMediaAudioCount:  metrics.Media.AudioCount,
		EstMediaAudioTokens: metrics.Media.AudioEst,
		EstMediaPDFCount:    metrics.Media.PDFCount,
		EstMediaPDFTokens:   metrics.Media.PDFEst,
	})
}

// currentModelSupportsVision returns whether the current thread's model supports vision.
func (t *Thread) currentModelSupportsVision() bool {
	mc := t.resolvedModelConfig()
	if mc != nil {
		return provider.SupportsVision(mc.Provider, mc.ModelType)
	}
	cfg := t.cfg()
	return provider.SupportsVision(cfg.ProviderName, cfg.ModelName)
}

func (t *Thread) currentModelSupportsAudio() bool {
	mc := t.resolvedModelConfig()
	if mc != nil {
		return provider.SupportsAudio(mc.Provider, mc.ModelType)
	}
	cfg := t.cfg()
	return provider.SupportsAudio(cfg.ProviderName, cfg.ModelName)
}

func (t *Thread) currentModelSupportsPDF() bool {
	mc := t.resolvedModelConfig()
	if mc != nil {
		return provider.SupportsPDF(mc.Provider, mc.ModelType)
	}
	cfg := t.cfg()
	return provider.SupportsPDF(cfg.ProviderName, cfg.ModelName)
}

// keepSupportedMedia filters wake-attached media markers down to those the
// resolved model can actually consume in user content. A marker whose type the
// model does not support would be silently dropped by the provider layer (the
// `!capable` continue in toOpenAIChatMessages / toGeminiContents); we surface
// that with a Warn instead so a mis-routed media wake (e.g. an audio marker
// sent to a non-audio model) is visible, not lost without a trace.
func (t *Thread) keepSupportedMedia(markers []string) []string {
	if len(markers) == 0 {
		return nil
	}
	kept := make([]string, 0, len(markers))
	for _, raw := range markers {
		_, parsed := provider.ParseMediaMarkers(raw)
		if len(parsed) == 0 {
			logger.Warn("wake media marker malformed, dropping", "sessionKey", t.sessionKey, "marker", raw)
			continue
		}
		m := parsed[0]
		supported := true
		switch {
		case strings.HasPrefix(m.MimeType, "image/"):
			supported = t.currentModelSupportsVision()
		case strings.HasPrefix(m.MimeType, "audio/"):
			supported = t.currentModelSupportsAudio()
		case m.MimeType == "application/pdf":
			supported = t.currentModelSupportsPDF()
		default:
			supported = false
		}
		if !supported {
			logger.Warn("wake media dropped: resolved model lacks capability for this media type",
				"sessionKey", t.sessionKey, "mime", m.MimeType, "file", m.FilePath)
			continue
		}
		kept = append(kept, raw)
	}
	return kept
}

// resolveProvider returns the provider for the current agent's model type,
// falling back to the default provider via factory (re-reads config each call
// so /init changes take effect immediately).
func (t *Thread) resolveProvider() provider.Provider {
	cfg := t.cfg()

	mc := t.resolvedModelConfig()
	if mc != nil && cfg.ProviderFactory != nil {
		p, err := cfg.ProviderFactory.Create(mc.Provider, mc.ModelType)
		if err == nil {
			return p
		}
		logger.Warn("failed to create provider, using default", "agent", t.Agent.Name, "model", mc.ModelType, "err", err)
	}

	// Always try factory for default provider (picks up config changes).
	if cfg.ProviderFactory != nil {
		p, err := cfg.ProviderFactory.Create("", "")
		if err == nil {
			return p
		}
	}

	return t.provider
}

// toolsForTurn returns the thread's tool registry, or an empty registry when the
// active agent declares disable_tools in its frontmatter (e.g. pre-think,
// media-preview). An empty registry means no tools are sent to the provider, the
// {{TOOLS}} placeholder is empty, and tool-def tokens are zero for this turn.
func (t *Thread) toolsForTurn() *tools.Registry {
	cfg := t.cfg()
	if t.Agent != nil && cfg.Agents != nil {
		if def := cfg.Agents.Def(t.Agent.Name); def != nil && def.DisableTools {
			return tools.NewRegistry()
		}
	}
	return t.tools
}

func (t *Thread) buildTools() *tools.Registry {
	cfg := t.cfg()
	reg := tools.NewRegistry()
	if cfg.Tools != nil {
		reg = cfg.Tools.Clone()
	}

	providerName, modelName := cfg.ProviderName, cfg.ModelName
	if mc := t.resolvedModelConfig(); mc != nil {
		providerName, modelName = mc.Provider, mc.ModelType
	}

	var logsDir string
	if cd, err := config.ConfigDir(); err == nil {
		logsDir = filepath.Join(cd, "logs")
	}

	reg.Register(&tools.HealthTool{
		Workspace:    cfg.Workspace,
		SessionsRoot: cfg.SessionsDir,
		SkillsRoot:   cfg.SkillsDir,
		LogsDir:      logsDir,
		ProviderName: providerName,
		ModelName:    modelName,
		ChannelsFn:   cfg.HealthChannelsFn,
		ThreadsListFn: func() []tools.ThreadInfo {
			return t.mgr.ListThreads()
		},
		CtxFn: func() tools.HealthRuntimeContext {
			sessionPath, _ := t.sessionFilePath() // ok ignored: empty path is acceptable
			t.mu.Lock()
			agentName := ""
			if t.Agent != nil {
				agentName = t.Agent.Name
			}
			t.mu.Unlock()
			pn, mn := t.resolvedProviderModel()
			return tools.HealthRuntimeContext{
				ThreadID:     t.id,
				AgentName:    agentName,
				SessionKey:   t.sessionKey,
				SessionFile:  sessionPath,
				ProviderName: pn,
				ModelName:    mn,
			}
		},
	})

	reg.Register(tools.NewDispatchTool(t))

	return reg
}

func (t *Thread) loadSession() *session.Session {
	cfg := t.cfg()
	if cfg.Sessions == nil || strings.TrimSpace(t.sessionKey) == "" {
		return nil
	}

	loadedSession, err := cfg.Sessions.Reload(t.sessionKey)
	if err != nil {
		logger.Warn("failed to load session", "key", t.sessionKey, "err", err)
		return nil
	}
	return loadedSession
}

func (t *Thread) buildSkillsSection() string {
	cfg := t.cfg()
	if cfg.Skills == nil || strings.TrimSpace(cfg.SkillsDir) == "" {
		return ""
	}

	// Load user first, then built-in (built-in overrides stale user copies on name conflict).
	dirs := []string{cfg.SkillsDir}
	if cfg.BuiltinSkillsDir != "" {
		dirs = append(dirs, cfg.BuiltinSkillsDir)
	}
	if err := cfg.Skills.ReloadFromDirectories(dirs...); err != nil {
		logger.Warn("failed to reload skills", "dirs", dirs, "err", err)
	}
	return cfg.Skills.BuildPromptSection()
}
