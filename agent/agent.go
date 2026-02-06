// Package agent provides prompt builders.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const timeLayout = "2006-01-02 15:04 (Monday)"

// PromptContext is the runtime context passed into prompt builders.
type PromptContext struct {
	Workspace string
	Time      time.Time
	ToolNames []string
	Skills    string
}

// Agent builds a system prompt for a thread run.
type Agent struct {
	Name         string
	BuildPrompt  func(PromptContext) string
	ProviderName string // Optional override, empty means use defaults
	ModelType    string // Optional override, empty means use defaults
}

// NewSoulAgent creates the default SOUL.md-based agent.
func NewSoulAgent(workspace string) *Agent {
	return &Agent{
		Name: "soul",
		BuildPrompt: func(ctx PromptContext) string {
			templatePath := filepath.Join(workspace, "SOUL.md")
			tpl, err := os.ReadFile(templatePath)
			if err != nil {
				logger.Warn("SOUL.md not found, using fallback prompt", "path", templatePath, "err", err)
				return fallbackPrompt(ctx)
			}

			userContent, _ := os.ReadFile(filepath.Join(workspace, "USER.md"))
			agentsContent := buildAgentsPromptSection(workspace)

			vars := map[string]string{
				"USER":   strings.TrimSpace(string(userContent)),
				"AGENTS": strings.TrimSpace(agentsContent),
			}

			return renderPrompt(stripFrontMatter(string(tpl)), ctx, vars)
		},
	}
}

// NewTemplateAgent creates an agent from any markdown template file.
func NewTemplateAgent(name, templatePath, workspace string) *Agent {
	return &Agent{
		Name: name,
		BuildPrompt: func(ctx PromptContext) string {
			tpl, err := os.ReadFile(templatePath)
			if err != nil {
				logger.Warn("agent template read failed, using fallback prompt", "name", name, "path", templatePath, "err", err)
				return fallbackPrompt(ctx)
			}

			userContent, _ := os.ReadFile(filepath.Join(workspace, "USER.md"))
			agentsContent := buildAgentsPromptSection(workspace)
			vars := map[string]string{
				"USER":   strings.TrimSpace(string(userContent)),
				"AGENTS": strings.TrimSpace(agentsContent),
			}

			return renderPrompt(stripFrontMatter(string(tpl)), ctx, vars)
		},
	}
}

// NewRawAgent creates an agent directly from a prompt string.
func NewRawAgent(name, prompt string) *Agent {
	return &Agent{
		Name: name,
		BuildPrompt: func(ctx PromptContext) string {
			return renderPrompt(prompt, ctx, nil)
		},
	}
}

func renderPrompt(tpl string, ctx PromptContext, vars map[string]string) string {
	if ctx.Time.IsZero() {
		ctx.Time = time.Now()
	}

	replacements := map[string]string{
		"TIME":      ctx.Time.Format(timeLayout),
		"WORKSPACE": ctx.Workspace,
		"TOOLS":     strings.Join(ctx.ToolNames, ", "),
		"SKILLS":    ctx.Skills,
		"USER":      "",
		"AGENTS":    "",
	}

	for k, v := range vars {
		replacements[k] = v
	}

	prompt := tpl
	for key, value := range replacements {
		prompt = strings.ReplaceAll(prompt, "{{"+key+"}}", value)
	}

	return prompt
}

func fallbackPrompt(ctx PromptContext) string {
	if ctx.Time.IsZero() {
		ctx.Time = time.Now()
	}

	return fmt.Sprintf(`You are nagobot, a helpful AI assistant.

Current Time: %s
Workspace: %s
Available Tools: %s

%s`,
		ctx.Time.Format(timeLayout),
		ctx.Workspace,
		strings.Join(ctx.ToolNames, ", "),
		ctx.Skills,
	)
}

func buildAgentsPromptSection(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	reg := NewRegistry(workspace)
	return reg.BuildPromptSection()
}
