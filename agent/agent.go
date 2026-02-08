// Package agent provides prompt builders.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const timeLayout = "2006-01-02 (Monday)"

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
	return NewTemplateAgent("soul", filepath.Join(workspace, "SOUL.md"), workspace)
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

	calendar := formatCalendar(ctx.Time)
	replacements := map[string]string{
		"TIME":      ctx.Time.Format(timeLayout),
		"CALENDAR":  calendar,
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
Calendar:
%s
Workspace: %s
Available Tools: %s

%s`,
		ctx.Time.Format(timeLayout),
		formatCalendar(ctx.Time),
		ctx.Workspace,
		strings.Join(ctx.ToolNames, ", "),
		ctx.Skills,
	)
}

func formatCalendar(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}

	location := now.Location().String()
	offset := now.Format("-07:00")

	var sb strings.Builder
	sb.WriteString("Timezone: ")
	sb.WriteString(location)
	sb.WriteString(" (UTC")
	sb.WriteString(offset)
	sb.WriteString(")\n")

	for delta := -7; delta <= 7; delta++ {
		day := now.AddDate(0, 0, delta)

		label := ""
		switch delta {
		case -1:
			label = "Yesterday, "
		case 0:
			label = "Today, "
		case 1:
			label = "Tomorrow, "
		}

		sb.WriteString(formatOffset(delta))
		sb.WriteString(": ")
		sb.WriteString(day.Format("2006-01-02"))
		sb.WriteString(" (")
		sb.WriteString(label)
		sb.WriteString(day.Weekday().String())
		sb.WriteString(")")
		if delta < 7 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func formatOffset(days int) string {
	abs := days
	sign := "+"
	if days < 0 {
		sign = "-"
		abs = -days
	}
	return sign + strconv.Itoa(abs) + "d"
}

func buildAgentsPromptSection(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	reg := NewRegistry(workspace)
	return reg.BuildPromptSection()
}
