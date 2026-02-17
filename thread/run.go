package thread

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/tools"
)

// run executes one thread turn. Called by RunOnce; callers must not invoke
// this directly.
func (t *Thread) run(ctx context.Context, userMessage string, sink Sink) (string, error) {
	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" {
		return "", nil
	}

	cfg := t.cfg()

	t.mu.Lock()
	activeAgent := t.Agent
	t.mu.Unlock()

	skillsSection := t.buildSkillsSection()

	systemPrompt := ""
	if activeAgent != nil {
		activeAgent.Set("TIME", time.Now())
		activeAgent.Set("TOOLS", t.tools.Names())
		activeAgent.Set("SKILLS", skillsSection)
		systemPrompt = activeAgent.Build()
	}
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = "You are a helpful AI assistant."
	}

	messages := make([]provider.Message, 0, 2)
	messages = append(messages, provider.SystemMessage(systemPrompt))

	sess := t.loadSession()
	if sess != nil {
		messages = append(messages, sess.Messages...)
	}

	turnUserMessages := make([]provider.Message, 0, 4)
	userMsg := provider.UserMessage(userMessage)
	messages = append(messages, userMsg)
	turnUserMessages = append(turnUserMessages, userMsg)

	sessionEstimatedTokens := 0
	if sess != nil {
		sessionEstimatedTokens = estimateMessagesTokens(sess.Messages)
	}
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

	sessionPath, _ := t.sessionFilePath()
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
		SessionKey: t.sessionKey,
		Workspace:  cfg.Workspace,
	})
	var intermediates []provider.Message
	runner := NewRunner(t.resolveProvider(), t.tools, metrics)
	runner.OnMessage(func(m provider.Message) {
		intermediates = append(intermediates, m)
		// Deliver intermediate assistant content (e.g. thinking aloud) to user in real time.
		if m.Role == "assistant" && isUserFacingContent(m.Content) && !sink.IsZero() && sink.Idempotent {
			_ = sink.Send(ctx, m.Content)
		}
	})
	response, err := runner.RunWithMessages(runCtx, messages)
	if err != nil {
		return "", err
	}

	// End-of-turn: append intermediate tool chain + final assistant response.
	if sess != nil {
		latestSession, reloadErr := t.reloadSessionForSave()
		if reloadErr != nil {
			logger.Warn(
				"failed to reload session before save; skipping save to avoid overwriting external changes",
				"key", t.sessionKey,
				"err", reloadErr,
			)
		} else {
			latestSession.Messages = append(latestSession.Messages, intermediates...)
			latestSession.Messages = append(latestSession.Messages, provider.AssistantMessage(response))
			if saveErr := cfg.Sessions.Save(latestSession); saveErr != nil {
				logger.Warn("failed to save session", "key", t.sessionKey, "err", saveErr)
			}
		}
	}

	return response, nil
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

// resolveProvider returns the provider for the current agent's model type,
// falling back to t.provider (the default set at thread creation).
func (t *Thread) resolveProvider() provider.Provider {
	cfg := t.cfg()
	if t.Agent == nil || cfg.Agents == nil {
		return t.provider
	}
	def := cfg.Agents.Def(t.Agent.Name)
	if def == nil || def.Model == "" {
		return t.provider
	}
	if cfg.ProviderFactory == nil || len(cfg.Models) == 0 {
		return t.provider
	}
	mc, ok := cfg.Models[def.Model]
	if !ok || mc == nil {
		logger.Warn("model type not mapped, using default", "agent", t.Agent.Name, "model", def.Model)
		return t.provider
	}
	p, err := cfg.ProviderFactory.Create(mc.Provider, mc.ModelType)
	if err != nil {
		logger.Warn("failed to create provider, using default", "agent", t.Agent.Name, "model", def.Model, "err", err)
		return t.provider
	}
	return p
}

func (t *Thread) buildTools() *tools.Registry {
	cfg := t.cfg()
	reg := tools.NewRegistry()
	if cfg.Tools != nil {
		reg = cfg.Tools.Clone()
	}

	reg.Register(&tools.HealthTool{
		Workspace:    cfg.Workspace,
		SessionsRoot: cfg.SessionsDir,
		SkillsRoot:   cfg.SkillsDir,
		ProviderName: cfg.ProviderName,
		ModelName:    cfg.ModelName,
		Channels:     cfg.HealthChannels,
		ThreadsListFn: func() []tools.ThreadInfo {
			return t.mgr.ListThreads()
		},
		CtxFn: func() tools.HealthRuntimeContext {
			sessionPath, _ := t.sessionFilePath()
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
