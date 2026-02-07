package thread

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/internal/runtimecfg"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/tools"
)

// Run executes one thread turn.
func (t *Thread) Run(ctx context.Context, userMessage string) (string, error) {
	t.runMu.Lock()
	defer t.runMu.Unlock()

	t.mu.Lock()
	activeAgent := t.agent
	activeSink := t.sink
	t.mu.Unlock()

	prov, err := t.resolveProvider()
	if err != nil {
		return "", err
	}

	runtimeTools := t.runtimeTools()
	skillsSection := t.buildSkillsSection()

	hasRealUserMessage := strings.TrimSpace(userMessage) != ""
	injectedUserMessages := []string{}
	if hasRealUserMessage {
		injectedUserMessages = append(injectedUserMessages, t.drainInjectQueue()...)
	}

	if pending := t.drainPendingResults(); pending != "" {
		injectedUserMessages = append(injectedUserMessages, pending)
		if !hasRealUserMessage {
			injectedUserMessages = append(injectedUserMessages, "No new user message. Continue based on async child thread results above and send the user a concise update.")
		}
	}

	if !hasRealUserMessage && len(injectedUserMessages) == 0 {
		return "", nil
	}

	promptCtx := agent.PromptContext{
		Workspace: t.workspace,
		Time:      time.Now(),
		ToolNames: runtimeTools.Names(),
		Skills:    skillsSection,
	}

	systemPrompt := ""
	if activeAgent != nil && activeAgent.BuildPrompt != nil {
		systemPrompt = activeAgent.BuildPrompt(promptCtx)
	}
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = agent.NewRawAgent("fallback", "You are a helpful AI assistant.").BuildPrompt(promptCtx)
	}

	messages := make([]provider.Message, 0, 2)
	messages = append(messages, provider.SystemMessage(systemPrompt))

	session := t.loadSession()
	if session != nil {
		messages = append(messages, session.Messages...)
	}

	turnUserMessages := make([]provider.Message, 0, len(injectedUserMessages)+2)
	for _, injected := range injectedUserMessages {
		msg := provider.UserMessage(injected)
		messages = append(messages, msg)
		turnUserMessages = append(turnUserMessages, msg)
	}
	if hasRealUserMessage {
		msg := provider.UserMessage(userMessage)
		messages = append(messages, msg)
		turnUserMessages = append(turnUserMessages, msg)

		// TODO: implement time system (timezone/user-locale aware runtime time injection).
		now := time.Now()
		timeMsg := provider.UserMessage(fmt.Sprintf(
			"[Runtime time note] Current server time: %s (timezone: %s, offset: UTC%s).",
			now.Format(time.RFC3339),
			now.Location().String(),
			now.Format("-07:00"),
		))
		messages = append(messages, timeMsg)
		turnUserMessages = append(turnUserMessages, timeMsg)
	}

	if len(turnUserMessages) == 0 {
		return "", nil
	}

	sessionEstimatedTokens := 0
	if session != nil {
		sessionEstimatedTokens = estimateMessagesTokens(session.Messages)
	}
	requestEstimatedTokens := estimateMessagesTokens(messages)
	contextWindowTokens, contextWarnRatio := t.contextBudget()
	logger.Debug(
		"context estimate",
		"threadID", t.id,
		"threadType", t.kind,
		"sessionKey", t.sessionKey,
		"injectedUserMessages", len(injectedUserMessages),
		"hasRealUserMessage", hasRealUserMessage,
		"sessionEstimatedTokens", sessionEstimatedTokens,
		"requestEstimatedTokens", requestEstimatedTokens,
		"contextWindowTokens", contextWindowTokens,
		"contextWarnRatio", contextWarnRatio,
	)

	sessionPath, _ := t.sessionFilePath()
	sessionSnapshot := []provider.Message{}
	if session != nil && len(session.Messages) > 0 {
		sessionSnapshot = make([]provider.Message, len(session.Messages))
		copy(sessionSnapshot, session.Messages)
	}
	requestSnapshot := make([]provider.Message, len(messages))
	copy(requestSnapshot, messages)
	injectedSnapshot := make([]string, len(injectedUserMessages))
	copy(injectedSnapshot, injectedUserMessages)
	t.runHooks(HookContext{
		ThreadID:               t.id,
		Type:                   t.kind,
		SessionKey:             t.sessionKey,
		SessionPath:            sessionPath,
		SessionMessages:        sessionSnapshot,
		InjectedMessages:       injectedSnapshot,
		UserMessage:            userMessage,
		RequestMessages:        requestSnapshot,
		SessionEstimatedTokens: sessionEstimatedTokens,
		RequestEstimatedTokens: requestEstimatedTokens,
		ContextWindowTokens:    contextWindowTokens,
		ContextWarnRatio:       contextWarnRatio,
	})

	runCtx := tools.WithRuntimeContext(ctx, tools.RuntimeContext{
		SessionKey: t.sessionKey,
	})
	runner := NewRunner(prov, runtimeTools)
	response, err := runner.RunWithMessages(runCtx, messages)
	if err != nil {
		return "", err
	}

	if session != nil {
		latestSession, reloadErr := t.reloadSessionForSave()
		if reloadErr != nil {
			logger.Warn(
				"failed to reload session before save; skipping save to avoid overwriting external changes",
				"key", t.sessionKey,
				"err", reloadErr,
			)
		} else {
			latestSession.Messages = append(latestSession.Messages, turnUserMessages...)
			latestSession.Messages = append(latestSession.Messages, provider.AssistantMessage(response))

			if saveErr := t.cfg.Sessions.Save(latestSession); saveErr != nil {
				logger.Warn("failed to save session", "key", t.sessionKey, "err", saveErr)
			}
		}
	}

	if activeSink != nil && !isSinkSuppressed(ctx) {
		if err := activeSink(ctx, response); err != nil {
			return "", err
		}
	}

	return response, nil
}

func (t *Thread) resolveProvider() (provider.Provider, error) {
	t.mu.Lock()
	activeAgent := t.agent
	t.mu.Unlock()

	if activeAgent != nil && (strings.TrimSpace(activeAgent.ProviderName) != "" || strings.TrimSpace(activeAgent.ModelType) != "") {
		if t.cfg.ProviderFactory == nil {
			return nil, fmt.Errorf("provider override requested but provider factory is not configured")
		}
		return t.cfg.ProviderFactory(activeAgent.ProviderName, activeAgent.ModelType)
	}

	if t.provider == nil {
		if t.cfg.ProviderFactory == nil {
			return nil, fmt.Errorf("default provider is not configured")
		}
		return t.cfg.ProviderFactory("", "")
	}

	return t.provider, nil
}

func (t *Thread) runtimeTools() *tools.Registry {
	runtimeTools := tools.NewRegistry()
	if t.tools != nil {
		runtimeTools = t.tools.Clone()
	}

	runtimeTools.Register(tools.NewHealthTool(t.workspace, func() tools.HealthRuntimeContext {
		sessionPath, _ := t.sessionFilePath()
		return tools.HealthRuntimeContext{
			ThreadID:    t.id,
			ThreadType:  string(t.kind),
			SessionKey:  t.sessionKey,
			SessionFile: sessionPath,
		}
	}))

	if t.allowSpawn {
		runtimeTools.Register(tools.NewSpawnThreadTool(t, t.agents))
		runtimeTools.Register(tools.NewCheckThreadTool(t))
	}

	return runtimeTools
}

func (t *Thread) loadSession() *Session {
	if t.cfg == nil || strings.TrimSpace(t.sessionKey) == "" || t.cfg.Sessions == nil {
		return nil
	}

	session, err := t.cfg.Sessions.Reload(t.sessionKey)
	if err != nil {
		logger.Warn("failed to load session", "key", t.sessionKey, "err", err)
		return nil
	}
	return session
}

func (t *Thread) reloadSessionForSave() (*Session, error) {
	if t.cfg == nil || strings.TrimSpace(t.sessionKey) == "" || t.cfg.Sessions == nil {
		return nil, fmt.Errorf("session manager unavailable")
	}
	return t.cfg.Sessions.Reload(t.sessionKey)
}

func (t *Thread) buildSkillsSection() string {
	if t.skills == nil || strings.TrimSpace(t.workspace) == "" {
		return ""
	}

	skillsDir := filepath.Join(t.workspace, runtimecfg.WorkspaceSkillsDirName)
	if err := t.skills.ReloadFromDirectory(skillsDir); err != nil {
		logger.Warn("failed to reload skills", "dir", skillsDir, "err", err)
	}
	return t.skills.BuildPromptSection()
}
