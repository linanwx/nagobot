// Package agent provides prompt builders.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

const dateLayout = "2006-01-02 (Monday)"

// Agent builds a system prompt for a thread run.
type Agent struct {
	Name      string
	workspace string
	loc       *time.Location // session timezone; nil = local
	vars      map[string]any // lazy placeholder overrides, applied at Build time
}

// SetLocation sets the timezone used for {{DATE}} and {{CALENDAR}} resolution.
func (a *Agent) SetLocation(loc *time.Location) {
	a.loc = loc
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
// {{DATE}} and {{CALENDAR}} are auto-resolved from the current time and agent timezone.
// For "TASK": if {{TASK}} is not found in the prompt, appends the task.
func (a *Agent) Build() string {
	if a == nil {
		return ""
	}
	prompt := a.readTemplate()

	if a.workspace != "" {
		// Expand CORE_MECHANISM first so its placeholders are available for subsequent replacements.
		coreContent, _ := os.ReadFile(filepath.Join(a.workspace, "system", "CORE_MECHANISM.md"))
		prompt = strings.ReplaceAll(prompt, "{{CORE_MECHANISM}}", strings.TrimSpace(string(coreContent)))
		prompt = strings.ReplaceAll(prompt, "{{WORKSPACE}}", a.workspace)
		// {{USER}} is resolved as a runtime var in thread/run.go (per-session).
		prompt = strings.ReplaceAll(prompt, "{{AGENTS}}", buildAgentsPromptSection(a.workspace))
		prompt = strings.ReplaceAll(prompt, "{{SESSIONS_SUMMARY}}", buildSessionsSummary(a.workspace))
		prompt = strings.ReplaceAll(prompt, "{{WORLD_KNOWLEDGE}}", buildWorldKnowledge(a.workspace))
	}

	// Auto-resolve {{DATE}} and {{CALENDAR}} from current time + agent timezone.
	now := time.Now()
	if a.loc != nil {
		now = now.In(a.loc)
	}
	prompt = strings.ReplaceAll(prompt, "{{DATE}}", now.Format(dateLayout))
	prompt = strings.ReplaceAll(prompt, "{{CALENDAR}}", formatCalendar(now))

	for key, value := range a.vars {
		formatted := formatVar(value)
		placeholder := "{{" + key + "}}"
		if strings.Contains(prompt, placeholder) {
			prompt = strings.ReplaceAll(prompt, placeholder, formatted)
		} else if key == "TASK" && strings.TrimSpace(formatted) != "" {
			prompt = strings.TrimSpace(prompt) + "\n\n[Task]\n" + formatted
		}
	}

	return prompt
}

// formatVar converts a var value to its string representation.
func formatVar(value any) string {
	switch v := value.(type) {
	case string:
		return v
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
	// Search builtin first (higher priority), then user agents.
	dirs := []string{
		filepath.Join(a.workspace, agentsBuiltinDir),
		filepath.Join(a.workspace, "agents"),
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, a.Name+".md")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		lower := filepath.Join(dir, strings.ToLower(a.Name)+".md")
		if _, err := os.Stat(lower); err == nil {
			return lower
		}
	}
	return filepath.Join(a.workspace, "agents", a.Name+".md") // fallback for error reporting
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

// buildWorldKnowledge reads system/world_knowledge.md and returns its content for prompt injection.
func buildWorldKnowledge(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return "(no world knowledge available)"
	}
	data, err := os.ReadFile(filepath.Join(workspace, "system", "world_knowledge.md"))
	if err != nil {
		return "(no world knowledge available)"
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "(no world knowledge available)"
	}
	return content
}

// buildSessionsSummary reads system/sessions_summary.json and formats it for prompt injection.
// Only sessions whose session.jsonl was modified within the last 2 days are included.
func buildSessionsSummary(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return "(no session summaries available)"
	}
	data, err := os.ReadFile(filepath.Join(workspace, "system", "sessions_summary.json"))
	if err != nil {
		return "(no session summaries available)"
	}

	type entry struct {
		Summary string `json:"summary"`
	}
	var summaries map[string]entry
	if err := json.Unmarshal(data, &summaries); err != nil || len(summaries) == 0 {
		return "(no session summaries available)"
	}

	cutoff := time.Now().AddDate(0, 0, -2)
	sessionsDir := filepath.Join(workspace, "sessions")

	// Sort keys for deterministic output (required for prompt caching).
	keys := make([]string, 0, len(summaries))
	for key := range summaries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, key := range keys {
		e := summaries[key]
		if strings.TrimSpace(e.Summary) == "" {
			continue
		}
		// Filter by session's last message timestamp (lightweight tail read).
		sessionPath := filepath.Join(sessionsDir, filepath.FromSlash(strings.ReplaceAll(key, ":", "/")), session.SessionFileName)
		ts, err := session.ReadUpdatedAt(sessionPath)
		if err != nil || ts.IsZero() || ts.Before(cutoff) {
			continue
		}
		fmt.Fprintf(&sb, "- %s: %s\n", key, e.Summary)
	}
	result := strings.TrimSpace(sb.String())
	if result == "" {
		return "(no session summaries available)"
	}
	return result
}
