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

	// Check for model chain before proceeding with single-model path.
	chain := t.resolvedChain()
	if len(chain) > 0 {
		return t.runChain(ctx, chain, userMessage, sink, injectFn)
	}

	cfg := t.cfg()
	systemPrompt := t.buildSystemPrompt()
	sess := t.loadSession()
	messages, turnUserMessages := t.buildMessageHistory(systemPrompt, userMessage, sess)

	// Write-ahead: persist user messages before LLM call so they survive a crash.
	if sess != nil {
		if waSess, waErr := t.reloadSessionForSave(); waErr == nil {
			waSess.Messages = append(waSess.Messages, turnUserMessages...)
			if saveErr := cfg.Sessions.Save(waSess); saveErr != nil {
				logger.Warn("write-ahead save failed", "key", t.sessionKey, "err", saveErr)
			}
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

	response, intermediates, err := t.executeRunner(ctx, runCtx, p, metrics, messages, sink, injectFn)
	if err != nil {
		return "", err
	}

	t.persistTurnMessages(cfg, sess, intermediates, response)
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
		sessionMessages = stripChainMessages(applyCompressed(provider.SanitizeMessages(sess.Messages)))
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
func (t *Thread) executeRunner(ctx, runCtx context.Context, p provider.Provider, metrics *ExecMetrics, messages []provider.Message, sink Sink, injectFn func() []provider.Message) (string, []provider.Message, error) {
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
	if err != nil {
		return "", nil, err
	}

	// Flush remaining streamed content and suppress final sink delivery.
	if streamer != nil {
		streamer.Flush()
		if streamer.Streamed() {
			t.SetSuppressSink()
		}
	}

	return response, intermediates, nil
}

// persistTurnMessages saves intermediate tool messages and final response to the session.
func (t *Thread) persistTurnMessages(cfg *ThreadConfig, sess *session.Session, intermediates []provider.Message, response string) {
	if sess == nil {
		return
	}
	latestSession, reloadErr := t.reloadSessionForSave()
	if reloadErr != nil {
		logger.Warn(
			"failed to reload session before save; skipping save to avoid overwriting external changes",
			"key", t.sessionKey,
			"err", reloadErr,
		)
		return
	}
	latestSession.Messages = append(latestSession.Messages, intermediates...)
	latestSession.Messages = append(latestSession.Messages, provider.AssistantMessage(response))
	if saveErr := cfg.Sessions.Save(latestSession); saveErr != nil {
		logger.Warn("failed to save session", "key", t.sessionKey, "err", saveErr)
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

// resolvedChain returns the chain steps for the current agent, or nil.
func (t *Thread) resolvedChain() []*config.ChainStep {
	mc := t.resolvedModelConfig()
	if mc == nil || len(mc.Chain) == 0 {
		return nil
	}
	return mc.Chain
}

// chainInfoPrefix is the XML tag used to identify chain-info messages for later stripping.
const chainInfoPrefix = "<chain-info"

// buildChainInfo builds the chain position prompt in XML format.
func buildChainInfo(chain []*config.ChainStep, currentIdx int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<chain-info position=\"%d\" total=\"%d\">\n", currentIdx+1, len(chain))
	sb.WriteString("  <models>\n")
	for i, step := range chain {
		fmt.Fprintf(&sb, "    <model position=\"%d\">%s</model>\n", i+1, step.ModelType)
	}
	sb.WriteString("  </models>\n")
	sb.WriteString("  <instruction>")
	if currentIdx == 0 {
		sb.WriteString("请快速回复、简单规划。后续模型会补充深度分析。")
	} else if currentIdx == len(chain)-1 {
		sb.WriteString("前序模型已回复（见上方对话）。请补充、深度调查、完善回答。")
	} else {
		sb.WriteString("前序模型已回复（见上方对话）。请从你的角度补充。")
	}
	sb.WriteString("</instruction>\n")
	sb.WriteString("</chain-info>")
	return sb.String()
}

// isChainMessage returns true if the message is a chain-info message.
func isChainMessage(m provider.Message) bool {
	return m.Role == "user" && strings.HasPrefix(strings.TrimSpace(m.Content), chainInfoPrefix)
}

// stripChainMessages removes chain-info messages from a message sequence.
func stripChainMessages(msgs []provider.Message) []provider.Message {
	result := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		if !isChainMessage(m) {
			result = append(result, m)
		}
	}
	return result
}

// runChain executes a model chain: each step uses a different provider/model.
func (t *Thread) runChain(ctx context.Context, chain []*config.ChainStep, userMessage string, sink Sink, injectFn func() []provider.Message) (string, error) {
	cfg := t.cfg()
	if cfg.ProviderFactory == nil {
		return noProviderMessage(), nil
	}
	var lastResponse string

	for i, step := range chain {
		chainInfoContent := buildChainInfo(chain, i)

		systemPrompt := t.buildSystemPrompt()
		sess := t.loadSession()

		var messages []provider.Message
		var turnUserMessages []provider.Message

		if i == 0 {
			// Step 0: original wake message + chain info as separate message
			messages, turnUserMessages = t.buildMessageHistory(systemPrompt, userMessage, sess)
			chainMsg := provider.UserMessage(chainInfoContent)
			messages = append(messages, chainMsg)
			turnUserMessages = append(turnUserMessages, chainMsg)
		} else {
			// Step 1+: chain info only (previous wake + response in session history)
			messages, turnUserMessages = t.buildMessageHistory(systemPrompt, chainInfoContent, sess)
		}

		// Write-ahead
		if sess != nil {
			if waSess, waErr := t.reloadSessionForSave(); waErr == nil {
				waSess.Messages = append(waSess.Messages, turnUserMessages...)
				if saveErr := cfg.Sessions.Save(waSess); saveErr != nil {
					logger.Warn("chain write-ahead save failed", "key", t.sessionKey, "step", i, "err", saveErr)
				}
			}
		}

		metrics := &ExecMetrics{TurnStart: time.Now()}
		t.mu.Lock()
		t.execMetrics = metrics
		t.mu.Unlock()

		runCtx := tools.WithRuntimeContext(ctx, tools.RuntimeContext{
			SessionKey:     t.sessionKey,
			Workspace:      cfg.Workspace,
			SupportsVision: provider.SupportsVision(step.Provider, step.ModelType),
		})

		// Reset halt and suppress for each step
		t.resetHaltLoop()
		t.mu.Lock()
		t.suppressSink = false
		t.mu.Unlock()

		p, err := cfg.ProviderFactory.Create(step.Provider, step.ModelType)
		if err != nil {
			logger.Warn("chain step provider failed, skipping", "step", i, "provider", step.Provider, "model", step.ModelType, "err", err)
			continue
		}

		// Only first step gets injectFn (mid-execution user messages)
		var stepInjectFn func() []provider.Message
		if i == 0 {
			stepInjectFn = injectFn
		}

		response, intermediates, runErr := t.executeRunner(ctx, runCtx, p, metrics, messages, sink, stepInjectFn)

		t.mu.Lock()
		t.execMetrics = nil
		t.mu.Unlock()

		if runErr != nil {
			return "", runErr
		}

		t.persistTurnMessages(cfg, sess, intermediates, response)
		lastResponse = response

		// Deliver if not already streamed
		suppress := t.checkAndResetSuppressSink()
		if !sink.IsZero() && strings.TrimSpace(response) != "" && !suppress {
			if sinkErr := sink.Send(ctx, response); sinkErr != nil {
				logger.Error("chain sink delivery error", "step", i, "err", sinkErr)
			}
		}
	}

	// If no step succeeded, return a fallback message.
	if lastResponse == "" {
		return noProviderMessage(), nil
	}

	// Suppress final delivery in RunOnce (we already delivered per-step)
	t.SetSuppressSink()
	return lastResponse, nil
}

func noProviderMessage() string {
	return `No LLM provider configured. To get started, send:

/init --provider openrouter --model moonshotai/kimi-k2.5 --api-key YOUR_KEY

Supported providers: openrouter, anthropic, deepseek, openai`
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
			return tools.HealthRuntimeContext{
				ThreadID:    t.id,
				AgentName:   agentName,
				SessionKey:  t.sessionKey,
				SessionFile: sessionPath,
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

func (t *Thread) reloadSessionForSave() (*session.Session, error) {
	cfg := t.cfg()
	if cfg.Sessions == nil || strings.TrimSpace(t.sessionKey) == "" {
		return nil, fmt.Errorf("session manager unavailable")
	}
	return cfg.Sessions.Reload(t.sessionKey)
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
