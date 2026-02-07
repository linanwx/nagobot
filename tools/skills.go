package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/provider"
)

// SkillProvider retrieves skill prompts.
type SkillProvider interface {
	GetSkillPrompt(name string) (string, bool)
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
						"description": "The skill name to load (for example: 'research').",
					},
				},
				"required": []string{"name"},
			},
		},
	}
}

// useSkillArgs are the arguments for use_skill.
type useSkillArgs struct {
	Name string `json:"name"`
}

// Run executes the tool.
func (t *UseSkillTool) Run(ctx context.Context, args json.RawMessage) string {
	var a useSkillArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	prompt, ok := t.provider.GetSkillPrompt(a.Name)
	if !ok {
		return fmt.Sprintf("Error: skill not found: %s", a.Name)
	}

	rt := RuntimeContextFrom(ctx)
	if strings.TrimSpace(rt.Workspace) != "" {
		prompt = strings.ReplaceAll(prompt, "{{WORKSPACE}}", rt.Workspace)
	}

	return prompt
}
