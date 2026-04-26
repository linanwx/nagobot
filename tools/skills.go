package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

// SkillProvider retrieves skill prompts.
type SkillProvider interface {
	GetSkillPrompt(name string) (prompt string, dir string, ok bool)
	SkillNames() []string
	Reload() error
}

// UseSkillTool loads the full prompt for a named skill.
type UseSkillTool struct {
	provider SkillProvider
}

// NewUseSkillTool creates a new use_skill tool.
func NewUseSkillTool(provider SkillProvider) *UseSkillTool {
	return &UseSkillTool{provider: provider}
}

// Def returns the tool definition.
func (t *UseSkillTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "use_skill",
			Description: "Load the instructions for a named skill. Use this when you need the guidance for a skill listed in your system prompt.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The skill name to load (for example: 'research'). Pass empty string to list all available skills.",
					},
				},
			},
		},
	}
}

// useSkillArgs are the arguments for use_skill.
type useSkillArgs struct {
	Name string `json:"name" required:"true"`
}

// Run executes the tool.
func (t *UseSkillTool) Run(ctx context.Context, args json.RawMessage) string {
	return withTimeout(ctx, "use_skill", skillToolTimeout, func(ctx context.Context) string {
		return t.run(ctx, args)
	})
}

func (t *UseSkillTool) run(ctx context.Context, args json.RawMessage) string {
	var a useSkillArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if a.Name == "" {
		names := t.provider.SkillNames()
		if len(names) == 0 {
			return "No skills available."
		}
		return fmt.Sprintf("Available skills: %s", strings.Join(names, ", "))
	}

	prompt, dir, ok := t.provider.GetSkillPrompt(a.Name)
	if !ok {
		// Skill not found — it may have been installed mid-turn.
		// Force a reload and retry once.
		if err := t.provider.Reload(); err == nil {
			prompt, dir, ok = t.provider.GetSkillPrompt(a.Name)
		}
		if !ok {
			names := t.provider.SkillNames()
			return fmt.Sprintf("Error: skill %q not found. Available skills: %s", a.Name, strings.Join(names, ", "))
		}
	}

	rt := RuntimeContextFrom(ctx)
	if strings.TrimSpace(rt.Workspace) != "" {
		prompt = strings.ReplaceAll(prompt, "{{WORKSPACE}}", rt.Workspace)
	}
	if strings.TrimSpace(rt.SessionDir) != "" {
		prompt = strings.ReplaceAll(prompt, "{{SESSIONDIR}}", rt.SessionDir)
	}
	if strings.TrimSpace(dir) != "" {
		prompt = strings.ReplaceAll(prompt, "{{SKILLDIR}}", dir)
	}

	header := skillHeader{Skill: a.Name}
	if dir != "" {
		header.Dir = dir
	}
	mapping, ok := msg.EncodeMapping(header)
	if !ok {
		return ""
	}
	return msg.BuildFrontmatter(mapping, "\n"+prompt)
}

type skillHeader struct {
	Skill string `yaml:"skill"`
	Dir   string `yaml:"dir,omitempty"`
}
