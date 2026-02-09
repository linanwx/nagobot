package thread

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/internal/runtimecfg"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/tools"
)

// run executes one thread turn. Called by RunOnce; callers must not invoke
// this directly.
func (t *Thread) run(ctx context.Context, userMessage string) (string, error) {
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
	hookInjections := t.runHooks(TurnContext{
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

	runCtx := tools.WithRuntimeContext(ctx, tools.RuntimeContext{
		SessionKey: t.sessionKey,
		Workspace:  cfg.Workspace,
	})
	runner := NewRunner(t.provider, t.tools)
	response, err := runner.RunWithMessages(runCtx, messages)
	if err != nil {
		return "", err
	}

	if sess != nil {
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

			if saveErr := cfg.Sessions.Save(latestSession); saveErr != nil {
				logger.Warn("failed to save session", "key", t.sessionKey, "err", saveErr)
			}
		}
	}

	return response, nil
}

func (t *Thread) buildTools() *tools.Registry {
	cfg := t.cfg()
	reg := tools.NewRegistry()
	if cfg.Tools != nil {
		reg = cfg.Tools.Clone()
	}

	reg.Register(tools.NewHealthTool(cfg.Workspace, func() tools.HealthRuntimeContext {
		sessionPath, _ := t.sessionFilePath()
		return tools.HealthRuntimeContext{
			ThreadID:    t.id,
			SessionKey:  t.sessionKey,
			SessionFile: sessionPath,
		}
	}))

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
	if cfg.Skills == nil || strings.TrimSpace(cfg.Workspace) == "" {
		return ""
	}

	skillsDir := filepath.Join(cfg.Workspace, runtimecfg.WorkspaceSkillsDirName)
	if err := cfg.Skills.ReloadFromDirectory(skillsDir); err != nil {
		logger.Warn("failed to reload skills", "dir", skillsDir, "err", err)
	}
	return cfg.Skills.BuildPromptSection()
}
