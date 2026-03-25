package thread

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/monitor"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/tools"
)

// run executes one thread turn. Called by RunOnce; callers must not invoke
// this directly.
func (t *Thread) run(ctx context.Context, userMessage string, sink Sink, injectFn func() []provider.Message, wakeSource string) (string, error) {
	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" {
		return "", nil
	}

	cfg := t.cfg()
	systemPrompt := t.buildSystemPrompt()
	sess := t.loadSession()
	messages, turnUserMessages := t.buildMessageHistory(ctx, systemPrompt, userMessage, sess)

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
		SupportsVision:        t.currentModelSupportsVision(),
		SupportsAudio:         t.currentModelSupportsAudio(),
		ImageReaderConfigured: cfg.Agents != nil && cfg.Agents.Def("imagereader") != nil,
		AudioReaderConfigured: cfg.Agents != nil && cfg.Agents.Def("audioreader") != nil,
	})
	t.resetHaltLoop()
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
		t.recordTurn(metrics, "", "", "", usage, true)
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
	t.recordTurn(metrics, providerName, modelName, agentName, usage, false)
	return response, nil
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
	activeAgent.Set("TOOLS", t.tools.Names())
	activeAgent.Set("SKILLS", skillsSection)
	activeAgent.Set("USER", t.buildUserSection())
	activeAgent.Set("HEARTBEAT", t.buildHeartbeatSection())
	prompt := activeAgent.Build()
	if strings.TrimSpace(prompt) == "" {
		return "You are a helpful AI assistant."
	}
	return prompt
}

// buildMessageHistory assembles the full message list for the LLM request,
// including system prompt, session history, user message, and hook injections.
// Returns the full messages slice and the turn-specific user messages (for write-ahead).
func (t *Thread) buildMessageHistory(ctx context.Context, systemPrompt, userMessage string, sess *session.Session) ([]provider.Message, []provider.Message) {
	messages := make([]provider.Message, 0, 2)
	messages = append(messages, provider.SystemMessage(systemPrompt))

	ct := t.contextBudget()
	contextWindowTokens := ct.ContextWindow

	// Compute precise session budget by subtracting known overhead from context window.
	systemPromptTokens := EstimateMessageTokens(messages[0])
	userMsgTokens := EstimateTextTokens(userMessage) + 6
	toolDefsTokens := EstimateToolDefsTokens(t.tools.Defs())
	maxCompletionTokens := t.cfg().MaxCompletionTokens
	sessionBudget := int(float64(contextWindowTokens-systemPromptTokens-userMsgTokens-toolDefsTokens-maxCompletionTokens) * 0.96)
	if sessionBudget < 0 {
		sessionBudget = 0
	}

	var sessionMessages []provider.Message
	var sessionEstimatedTokens int
	if sess != nil {
		sessionMessages = ApplyCompressed(provider.SanitizeMessages(sess.Messages))
		sessionMessages, sessionEstimatedTokens = t.applyTier0Truncation(sessionMessages, sessionBudget)
		messages = append(messages, sessionMessages...)
	}

	turnUserMessages := make([]provider.Message, 0, 4)
	userMsg := provider.UserMessage(userMessage)
	messages = append(messages, userMsg)
	turnUserMessages = append(turnUserMessages, userMsg)

	requestEstimatedTokens := sessionEstimatedTokens + EstimateMessageTokens(messages[0]) + EstimateMessageTokens(userMsg) + toolDefsTokens + 3
	logger.Debug(
		"context estimate",
		"threadID", t.id,
		"sessionKey", t.sessionKey,
		"sessionEstimatedTokens", sessionEstimatedTokens,
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
		SessionEstimatedTokens: sessionEstimatedTokens,
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
	loopBudget := int(float64(contextWindowTokens-maxCompletionTokens) * 0.9)
	if loopBudget < 0 {
		loopBudget = 0
	}
	runner := NewRunner(p, t.tools, metrics, loopBudget)
	runner.ShouldHalt(t.isHaltLoop)
	runner.SetUserVisible(sysmsg.IsUserVisibleSource(t.lastWakeSource))

	// Set up streaming for chunkable sinks (Telegram, Discord, Feishu, CLI).
	var streamer *MarkdownStreamer
	var chatStreamed bool // whether current Chat() round produced streaming deltas
	if !sink.IsZero() && sink.Chunkable {
		streamer = NewMarkdownStreamer(sink, ctx, streamFlushThreshold)
		runner.OnText(func(delta string) {
			if ctx.Err() != nil {
				return
			}
			if !t.isSuppressSink() {
				chatStreamed = true
				streamer.OnDelta(delta)
			}
		})
		runner.OnChatEnd(func() {
			if ctx.Err() != nil {
				return
			}
			if !t.isSuppressSink() {
				streamer.Flush()
			}
		})
	}

	var hadToolCalls bool // tracks whether any tool calls occurred during the entire run
	runner.OnMessage(func(m provider.Message) {
		intermediates = append(intermediates, m)
		// Incremental persistence: save each message to disk as it arrives.
		if persistMsg != nil {
			persistMsg(m)
		}
		// Track tool calls for SLEEP_THREAD_OK fallback detection.
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			hadToolCalls = true
		}
		// Deliver intermediate assistant content (with tool_calls) to user in real time.
		// Final response delivery is handled by onFinalResponse — only intermediate
		// messages (those with tool_calls) are delivered here to avoid double delivery.
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && isUserFacingContent(m.Content) && !sink.IsZero() && sink.Chunkable && !chatStreamed && !t.isSuppressSink() {
			_ = sink.Send(ctx, m.Content)
		}
		if m.Role == "assistant" {
			chatStreamed = false
		}
	})

	// Deliver final response (no tool calls) inside the runner lifecycle.
	// For streaming: streamer already delivered via OnText chunks — skip.
	// For non-streaming or when streamer didn't fire: deliver via sink.Send.
	// WithRetry(3) only wraps final delivery, not streaming chunks.
	runner.OnFinalResponse(func(content string) {
		// SLEEP_THREAD_OK text marker fallback: when a model with weak tool-calling
		// outputs this marker instead of calling sleep_thread(), treat it as equivalent
		// to suppress sink delivery during heartbeat turns.
		if t.IsHeartbeatWake() && !hadToolCalls && strings.Contains(content, "SLEEP_THREAD_OK") {
			t.SetSuppressSink()
			logger.Info("SLEEP_THREAD_OK fallback triggered", "sessionKey", t.sessionKey, "source", t.lastWakeSource)
		}
		if sink.IsZero() || t.isSuppressSink() || !isUserFacingContent(content) {
			return
		}
		if streamer != nil && streamer.Streamed() {
			return
		}
		_ = sink.WithRetry(3).Send(ctx, content)
	})

	runner.OnIterationEnd(injectFn)
	response, err = runner.RunWithMessages(runCtx, messages)
	usage = runner.TotalUsage()
	providerLabel = runner.ProviderLabel()
	modelLabel = runner.ModelLabel()
	if err != nil {
		return "", nil, usage, nil, "", "", err
	}

	return response, intermediates, usage, runner.LastQuota(), providerLabel, modelLabel, nil
}

// buildUserSection resolves the per-session USER.md into a formatted section.
func (t *Thread) buildUserSection() string {
	sessionPath, ok := t.sessionFilePath()
	if !ok {
		return "## User Preferences\n\nNo session path available."
	}
	userPath := filepath.Join(filepath.Dir(sessionPath), "USER.md")
	absPath, _ := filepath.Abs(userPath)

	content, err := os.ReadFile(userPath)
	if err != nil {
		return fmt.Sprintf("## User Preferences\n\n`%s` does not exist. Create it to store user preferences.", absPath)
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return fmt.Sprintf("## User Preferences\n\n`%s` is empty. Append to store user preferences.", absPath)
	}
	return fmt.Sprintf("## User Preferences\n\nCurrently using `%s` as preferences. Append to store.\n\n%s", absPath, text)
}

// buildHeartbeatSection resolves the per-session heartbeat.md path into a formatted section.
// Content is NOT included — heartbeat.md changes frequently and would break prompt caching.
func (t *Thread) buildHeartbeatSection() string {
	sessionPath, ok := t.sessionFilePath()
	if !ok {
		return ""
	}
	hbPath := filepath.Join(filepath.Dir(sessionPath), "heartbeat.md")
	absPath, _ := filepath.Abs(hbPath)
	return fmt.Sprintf("## Heartbeat\n\nHeartbeat automatically wakes the thread to reflect on follow-up items and proactively help users with tasks.\n\nCurrently using `%s`.\n\nUse `use_skill(heartbeat-reflect)` to track items and `use_skill(heartbeat-act)` to proactively help users.", absPath)
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
			m.ReasoningDetails = nil
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
		m.ReasoningDetails = nil
	}
	return m
}

// resolvedModelConfig returns the model config for the current agent's model type,
// or nil if the agent uses the default provider.
// Uses ModelsFn for hot-reload if available, falling back to the startup snapshot.
func (t *Thread) resolvedModelConfig() *config.ModelConfig {
	cfg := t.cfg()
	if t.Agent == nil || cfg.Agents == nil {
		return nil
	}
	def := cfg.Agents.Def(t.Agent.Name)
	if def == nil || def.Specialty == "" {
		return nil
	}
	models := cfg.Models
	if cfg.ModelsFn != nil {
		models = cfg.ModelsFn()
	}
	// Explicit routing table lookup.
	if len(models) > 0 {
		if mc, ok := models[def.Specialty]; ok && mc != nil {
			return mc
		}
	}
	// Implicit: specialty "provider/model" format → auto-route.
	if prov, model, ok := strings.Cut(def.Specialty, "/"); ok && provider.IsSupportedModel(model) {
		return &config.ModelConfig{
			Provider:  prov,
			ModelType: model,
		}
	}
	// Implicit: bare model name with provider from frontmatter or registry lookup.
	if provider.IsSupportedModel(def.Specialty) {
		prov := def.Provider
		if prov == "" {
			prov = provider.ProviderForModel(def.Specialty)
		}
		if prov != "" {
			return &config.ModelConfig{
				Provider:  prov,
				ModelType: def.Specialty,
			}
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
	return cfg.ProviderName, cfg.ModelName
}

// recordTurn writes a TurnRecord to the metrics store if available.
func (t *Thread) recordTurn(metrics *ExecMetrics, providerName, modelName, agentName string, usage provider.Usage, isError bool) {
	cfg := t.cfg()
	if cfg.MetricsStore == nil || metrics == nil {
		return
	}
	cfg.MetricsStore.Record(monitor.TurnRecord{
		Timestamp:        metrics.TurnStart,
		DurationMs:       time.Since(metrics.TurnStart).Milliseconds(),
		Provider:         providerName,
		Model:            modelName,
		Agent:            agentName,
		SessionKey:       t.sessionKey,
		Iterations:       metrics.Iterations,
		ToolCalls:        metrics.TotalToolCalls,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		CachedTokens:     usage.CachedTokens,
		Error:            isError,
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

	reg.Register(tools.NewSpawnThreadTool(t))
	reg.Register(tools.NewSleepThreadTool(t))

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
