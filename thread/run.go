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
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/tools"
)

// run executes one thread turn.
func (t *Thread) run(ctx context.Context, userMessage string) (string, error) {
	t.runMu.Lock()
	defer t.runMu.Unlock()

	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" {
		return "", nil
	}

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
	if runtimeContext := t.buildRuntimeContext(); runtimeContext != "" {
		systemPrompt = strings.TrimSpace(systemPrompt) + "\n\n" + runtimeContext
	}

	messages := make([]provider.Message, 0, 2)
	messages = append(messages, provider.SystemMessage(systemPrompt))

	session := t.loadSession()
	if session != nil {
		messages = append(messages, session.Messages...)
	}

	turnUserMessages := make([]provider.Message, 0, 4)
	userMsg := provider.UserMessage(userMessage)
	messages = append(messages, userMsg)
	turnUserMessages = append(turnUserMessages, userMsg)

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
		"sessionEstimatedTokens", sessionEstimatedTokens,
		"requestEstimatedTokens", requestEstimatedTokens,
		"contextWindowTokens", contextWindowTokens,
		"contextWarnRatio", contextWarnRatio,
	)

	sessionPath, _ := t.sessionFilePath()
	hookInjections := t.runHooks(TurnContext{
		ThreadID:               t.id,
		ThreadType:             t.kind,
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

	runCtx := tools.WithRuntimeContext(ctx, tools.RuntimeContext{
		SessionKey: t.sessionKey,
		Workspace:  t.workspace,
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

func (t *Thread) buildRuntimeContext() string {
	t.mu.Lock()
	threadID := t.id
	threadType := t.kind
	sessionKey := strings.TrimSpace(t.sessionKey)
	sinkLabel := strings.TrimSpace(t.sinkLabel)
	sinkRegistered := t.sink != nil
	t.mu.Unlock()

	if sessionKey == "" {
		sessionKey = "none"
	}
	if sinkLabel == "" {
		sinkLabel = "none"
	}

	return fmt.Sprintf(
		"[Thread Runtime Context]\n- thread_id: %s\n- thread_type: %s\n- session_key: %s\n- sink_label: %s\n- sink_registered: %t",
		threadID,
		threadType,
		sessionKey,
		sinkLabel,
		sinkRegistered,
	)
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

func (t *Thread) loadSession() *session.Session {
	if t.cfg == nil || strings.TrimSpace(t.sessionKey) == "" || t.cfg.Sessions == nil {
		return nil
	}

	loadedSession, err := t.cfg.Sessions.Reload(t.sessionKey)
	if err != nil {
		logger.Warn("failed to load session", "key", t.sessionKey, "err", err)
		return nil
	}
	return loadedSession
}

func (t *Thread) reloadSessionForSave() (*session.Session, error) {
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
