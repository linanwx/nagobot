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

// Agent builds a system prompt for a thread run.
type Agent struct {
	Name      string
	workspace string
	vars      map[string]any // lazy placeholder overrides, applied at Build time
}

// Set records a placeholder replacement applied lazily at Build time.
// Supported value types: string, time.Time, []string.
func (a *Agent) Set(key string, value any) *Agent {
	if a.vars == nil {
		a.vars = make(map[string]any)
	}
	a.vars[key] = value
	return a
}

// Build constructs the final prompt: reads template, applies vars.
// For "TASK": if {{TASK}} is not found in the prompt, appends the task.
// For time.Time values: also replaces {{CALENDAR}} automatically.
func (a *Agent) Build() string {
	if a == nil {
		return ""
	}
	prompt := a.readTemplate()

	if a.workspace != "" {
		prompt = strings.ReplaceAll(prompt, "{{WORKSPACE}}", a.workspace)
		userContent, _ := os.ReadFile(filepath.Join(a.workspace, "USER.md"))
		prompt = strings.ReplaceAll(prompt, "{{USER}}", strings.TrimSpace(string(userContent)))
		prompt = strings.ReplaceAll(prompt, "{{AGENTS}}", buildAgentsPromptSection(a.workspace))
		coreContent, _ := os.ReadFile(filepath.Join(a.workspace, "CORE_MECHANISM.md"))
		prompt = strings.ReplaceAll(prompt, "{{CORE_MECHANISM}}", strings.TrimSpace(string(coreContent)))
	}

	for key, value := range a.vars {
		formatted := formatVar(value)
		placeholder := "{{" + key + "}}"
		if strings.Contains(prompt, placeholder) {
			prompt = strings.ReplaceAll(prompt, placeholder, formatted)
		} else if key == "TASK" && strings.TrimSpace(formatted) != "" {
			prompt = strings.TrimSpace(prompt) + "\n\n[Task]\n" + formatted
		}
		if t, ok := value.(time.Time); ok && !t.IsZero() {
			prompt = strings.ReplaceAll(prompt, "{{CALENDAR}}", formatCalendar(t))
		}
	}

	return prompt
}

// formatVar converts a var value to its string representation.
func formatVar(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case time.Time:
		if v.IsZero() {
			v = time.Now()
		}
		return v.Format(timeLayout)
	case []string:
		return strings.Join(v, ", ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// newAgent creates an agent. Template path: workspace/agents/<name>.md.
func newAgent(name, workspace string) *Agent {
	return &Agent{
		Name:      name,
		workspace: workspace,
	}
}

func (a *Agent) templatePath() string {
	if a.workspace == "" {
		return ""
	}
	return filepath.Join(a.workspace, "agents", a.Name+".md")
}

func (a *Agent) readTemplate() string {
	path := a.templatePath()
	if path == "" {
		return "You are nagobot, a helpful AI assistant."
	}
	tpl, err := os.ReadFile(path)
	if err != nil {
		logger.Warn("agent template read failed, using fallback prompt", "name", a.Name, "path", path, "err", err)
		return "You are nagobot, a helpful AI assistant."
	}
	return stripFrontMatter(string(tpl))
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
