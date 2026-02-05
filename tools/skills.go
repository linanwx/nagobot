package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/provider"
)

// SkillProvider retrieves skill prompts and checks requirements.
type SkillProvider interface {
	GetSkillPrompt(name string) (string, bool)
	CheckRequirements(name string) (met bool, missing []string)
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
			Description: "Load the full instructions for a named skill. Checks prerequisites before loading. Use this when you need the detailed prompt for a skill listed in your system prompt.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The skill name to load (e.g., 'code-review', 'git-commit').",
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
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	prompt, ok := t.provider.GetSkillPrompt(a.Name)
	if !ok {
		return fmt.Sprintf("Error: skill not found or disabled: %s", a.Name)
	}

	// Check prerequisites
	met, missing := t.provider.CheckRequirements(a.Name)
	if !met {
		return fmt.Sprintf("Warning: skill '%s' has unmet prerequisites:\n- %s\n\nSkill instructions (may not work fully):\n\n%s",
			a.Name, strings.Join(missing, "\n- "), prompt)
	}

	return prompt
}
