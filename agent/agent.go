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
)

const dateLayout = "2006-01-02 (Monday)"

// Agent builds a system prompt for a thread run.
type Agent struct {
	Name      string
	workspace string
	loc       *time.Location    // session timezone; nil = local
	vars      map[string]any    // lazy placeholder overrides, applied at Build time
	meta      TemplateMeta      // parsed frontmatter (includes Sections)
	sections  *SectionRegistry  // shared core section registry
}

// SetSections sets the shared SectionRegistry for core section assembly.
func (a *Agent) SetSections(s *SectionRegistry) {
	a.sections = s
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

// Build constructs the final prompt via a 4-stage pipeline:
//  1. Agent personality (read template)
//  2. Core sections (auto-append from SectionRegistry)
//  3. Per-session sections (append those declared in frontmatter Sections)
//  4. Resolve all remaining placeholders
func (a *Agent) Build() string {
	if a == nil {
		return ""
	}

	// ── Stage 1: Agent personality ──
	body := a.readTemplate()
	agentHeader := fmt.Sprintf("---\ntype: agent_identity\nfile_path: %s\nprompt: This is your identity and behavioral guidelines.\n---", a.templatePath())
	prompt := agentHeader + "\n\n" + strings.TrimSpace(body)

	// ── Stage 2: Core sections (unconditional auto-append) ──
	if a.sections != nil {
		a.sections.Reload()
		if a.sections.Count() > 0 {
			coreHeader := "---\ntype: core_mechanism\nfile_path: internal\nprompt: This describes how the nagobot system works.\n---"
			prompt = strings.TrimSpace(prompt) + "\n\n" + coreHeader + "\n\n" + a.sections.Assemble()
		}
	}

	// ── Stage 3: File-backed blocks (own YAML header with file path) ──
	if a.workspace != "" {
		// World Knowledge — written by cron, updated periodically.
		wkContent := buildWorldKnowledge(a.workspace)
		if wkContent != "" {
			wkPath, _ := filepath.Abs(filepath.Join(a.workspace, "system", "world_knowledge.md"))
			wkHeader := fmt.Sprintf("---\ntype: world_knowledge\nfile_path: %s\nprompt: Recent events beyond model training cutoff.\n---", wkPath)
			prompt += "\n\n" + wkHeader + "\n\n" + wkContent
		}

		// Global instruction — user-editable, never overwritten by onboard --sync.
		globalContent := buildGlobal(a.workspace)
		if globalContent != "" {
			globalPath, _ := filepath.Abs(filepath.Join(a.workspace, "system", "GLOBAL.md"))
			globalHeader := fmt.Sprintf("---\ntype: global_instruction\nfile_path: %s\nprompt: follow the instruction\n---", globalPath)
			prompt += "\n\n" + globalHeader + "\n\n" + globalContent
		}
	}

	// ── Stage 4: Per-session sections (frontmatter opt-in) ──
	var consumed map[string]bool
	if len(a.meta.Sections) > 0 {
		consumed = make(map[string]bool, len(a.meta.Sections))
		for _, name := range a.meta.Sections {
			if val, ok := a.vars[name]; ok {
				formatted := formatVar(val)
				if strings.TrimSpace(formatted) != "" {
					prompt += "\n\n" + formatted
				}
				consumed[name] = true
			}
		}
	}

	// ── Stage 5: Resolve all remaining placeholders ──
	if a.workspace != "" {
		prompt = strings.ReplaceAll(prompt, "{{WORKSPACE}}", a.workspace)
		prompt = strings.ReplaceAll(prompt, "{{AGENTS}}", buildAgentsPromptSection(a.workspace))
		prompt = strings.ReplaceAll(prompt, "{{SESSIONS_SUMMARY}}", buildSessionsSummary(a.workspace))
	}

	now := time.Now()
	if a.loc != nil {
		now = now.In(a.loc)
	}
	prompt = strings.ReplaceAll(prompt, "{{DATE}}", now.Format(dateLayout))
	prompt = strings.ReplaceAll(prompt, "{{CALENDAR}}", formatCalendar(now))

	for key, value := range a.vars {
		if consumed != nil && consumed[key] {
			continue
		}
		formatted := formatVar(value)
		placeholder := "{{" + key + "}}"
		if strings.Contains(prompt, placeholder) {
			prompt = strings.ReplaceAll(prompt, placeholder, formatted)
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
	meta, body, hasHeader, _ := ParseTemplate(string(tpl))
	if hasHeader {
		a.meta = meta
	}
	return strings.TrimLeft(body, "\n")
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

// buildGlobal reads system/GLOBAL.md and returns its content for prompt injection.
// This file is never overwritten by onboard --sync, providing a stable customization point.
func buildGlobal(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(workspace, "system", "GLOBAL.md"))
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	return content
}

// buildSessionsSummary reads system/sessions_summary.json and formats it for prompt injection.
// Child-thread keys (containing ":threads:") are excluded — only parent sessions get rendered.
// No time-based filtering: stability of the rendered output is critical for prompt caching.
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

	keys := make([]string, 0, len(summaries))
	for key := range summaries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, key := range keys {
		if strings.Contains(key, ":threads:") {
			continue
		}
		e := summaries[key]
		if strings.TrimSpace(e.Summary) == "" {
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
