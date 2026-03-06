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
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/tools"
)

// run executes one thread turn. Called by RunOnce; callers must not invoke
// this directly.
func (t *Thread) run(ctx context.Context, userMessage string, sink Sink, injectFn func() []provider.Message) (string, error) {
	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" {
		return "", nil
	}

	cfg := t.cfg()
	systemPrompt := t.buildSystemPrompt()
	sess := t.loadSession()
	messages, turnUserMessages := t.buildMessageHistory(systemPrompt, userMessage, sess)

	// Write-ahead: persist user messages before LLM call so they survive a crash.
	if sess != nil {
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
		SessionKey:     t.sessionKey,
		Workspace:      cfg.Workspace,
		SupportsVision: t.currentModelSupportsVision(),
	})
	t.resetHaltLoop()
	p := t.resolveProvider()
	if p == nil {
		return noProviderMessage(), nil
	}

	response, intermediates, usage, err := t.executeRunner(ctx, runCtx, p, metrics, messages, sink, injectFn)
	if err != nil {
		t.recordTurn(metrics, "", "", "", usage, true)
		return "", err
	}

	t.persistTurnMessages(cfg, sess, intermediates, response)
	providerName, modelName := t.resolvedProviderModel()
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
	activeAgent.Set("TIME", t.now())
	activeAgent.Set("TOOLS", t.tools.Names())
	activeAgent.Set("SKILLS", skillsSection)
	activeAgent.Set("USER", t.buildUserSection())
	prompt := activeAgent.Build()
	if strings.TrimSpace(prompt) == "" {
		return "You are a helpful AI assistant."
	}
	return prompt
}

// buildMessageHistory assembles the full message list for the LLM request,
// including system prompt, session history, user message, and hook injections.
// Returns the full messages slice and the turn-specific user messages (for write-ahead).
func (t *Thread) buildMessageHistory(systemPrompt, userMessage string, sess *session.Session) ([]provider.Message, []provider.Message) {
	messages := make([]provider.Message, 0, 2)
	messages = append(messages, provider.SystemMessage(systemPrompt))

	var sessionMessages []provider.Message
	if sess != nil {
		sessionMessages = applyCompressed(provider.SanitizeMessages(sess.Messages))
		messages = append(messages, sessionMessages...)
	}

	turnUserMessages := make([]provider.Message, 0, 4)
	userMsg := provider.UserMessage(userMessage)
	messages = append(messages, userMsg)
	turnUserMessages = append(turnUserMessages, userMsg)

	sessionEstimatedTokens := estimateMessagesTokens(sessionMessages)
	requestEstimatedTokens := estimateMessagesTokens(messages)
	contextWindowTokens, contextWarnRatio := t.contextBudget()
	logger.Debug(
		"context estimate",
		"threadID", t.id,
		"sessionKey", t.sessionKey,
		"sessionEstimatedTokens", sessionEstimatedTokens,
		"requestEstimatedTokens", requestEstimatedTokens,
		"contextWindowTokens", contextWindowTokens,
		"contextWarnRatio", contextWarnRatio,
	)

	sessionPath, _ := t.sessionFilePath() // ok ignored: empty path is acceptable for hooks
	hookInjections := t.runHooks(turnContext{
		ThreadID:               t.id,
		SessionKey:             t.sessionKey,
		SessionPath:            sessionPath,
		UserMessage:            userMessage,
		SessionEstimatedTokens: sessionEstimatedTokens,
		RequestEstimatedTokens: requestEstimatedTokens,
		ContextWindowTokens:    contextWindowTokens,
		ContextWarnRatio:       contextWarnRatio,
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
func (t *Thread) executeRunner(ctx, runCtx context.Context, p provider.Provider, metrics *ExecMetrics, messages []provider.Message, sink Sink, injectFn func() []provider.Message) (string, []provider.Message, provider.Usage, error) {
	var intermediates []provider.Message
	runner := NewRunner(p, t.tools, metrics)
	runner.ShouldHalt(t.isHaltLoop)

	// Set up streaming for idempotent sinks (Telegram, Discord, Feishu, CLI).
	var streamer *MarkdownStreamer
	if !sink.IsZero() && sink.Idempotent {
		streamer = NewMarkdownStreamer(sink, ctx, streamFlushThreshold)
		runner.OnText(func(delta string) {
			if !t.isSuppressSink() {
				streamer.OnDelta(delta)
			}
		})
		runner.OnChatEnd(func() {
			if !t.isSuppressSink() {
				streamer.Flush()
			}
		})
	}

	runner.OnMessage(func(m provider.Message) {
		intermediates = append(intermediates, m)
		// Deliver intermediate assistant content to user in real time.
		// Skip when streaming is active — streamer handles delivery via OnTextDelta.
		if streamer == nil && m.Role == "assistant" && isUserFacingContent(m.Content) && !sink.IsZero() && sink.Idempotent {
			_ = sink.Send(ctx, m.Content)
		}
	})
	runner.OnIterationEnd(injectFn)
	response, err := runner.RunWithMessages(runCtx, messages)
	usage := runner.TotalUsage()
	if err != nil {
		return "", nil, usage, err
	}

	// Flush remaining streamed content and suppress final sink delivery.
	if streamer != nil {
		streamer.Flush()
		if streamer.Streamed() {
			t.SetSuppressSink()
		}
	}

	return response, intermediates, usage, nil
}

// persistTurnMessages saves intermediate tool messages and final response to the session.
func (t *Thread) persistTurnMessages(cfg *ThreadConfig, sess *session.Session, intermediates []provider.Message, response string) {
	if sess == nil {
		return
	}
	toAppend := make([]provider.Message, 0, len(intermediates)+1)
	toAppend = append(toAppend, intermediates...)
	toAppend = append(toAppend, provider.AssistantMessage(response))
	if err := cfg.Sessions.Append(t.sessionKey, toAppend...); err != nil {
		logger.Warn("failed to save session", "key", t.sessionKey, "err", err)
	}
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

// applyCompressed returns a copy of messages with Compressed content applied.
// For messages that have a Compressed field, Content is replaced so the LLM
// sees the compressed version. The original session data is not modified.
func applyCompressed(msgs []provider.Message) []provider.Message {
	result := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		if m.Compressed != "" {
			m.Content = m.Compressed
		}
		result[i] = m
	}
	return result
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
	if len(models) == 0 {
		return nil
	}
	mc, ok := models[def.Specialty]
	if !ok || mc == nil {
		return nil
	}
	return mc
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

	reg.Register(&tools.HealthTool{
		Workspace:    cfg.Workspace,
		SessionsRoot: cfg.SessionsDir,
		SkillsRoot:   cfg.SkillsDir,
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

	if err := cfg.Skills.ReloadFromDirectory(cfg.SkillsDir); err != nil {
		logger.Warn("failed to reload skills", "dir", cfg.SkillsDir, "err", err)
	}
	return cfg.Skills.BuildPromptSection()
}
