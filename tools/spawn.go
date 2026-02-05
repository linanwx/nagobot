package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pinkplumcom/nagobot/bus"
	"github.com/pinkplumcom/nagobot/provider"
)

// SpawnAgentTool spawns a subagent to handle a task.
type SpawnAgentTool struct {
	manager  *bus.SubagentManager
	parentID string
}

// NewSpawnAgentTool creates a new spawn agent tool.
func NewSpawnAgentTool(manager *bus.SubagentManager, parentID string) *SpawnAgentTool {
	return &SpawnAgentTool{
		manager:  manager,
		parentID: parentID,
	}
}

// Def returns the tool definition.
func (t *SpawnAgentTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "spawn_agent",
			Description: "Spawn a subagent to handle a specific task. The subagent runs in parallel and returns results when done. Use for delegating complex subtasks.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"description": "The type of subagent: 'research' (web search/fetch), 'code' (code analysis/generation), 'review' (code review).",
						"enum":        []string{"research", "code", "review"},
					},
					"task": map[string]any{
						"type":        "string",
						"description": "The task description for the subagent.",
					},
					"context": map[string]any{
						"type":        "string",
						"description": "Optional additional context for the task.",
					},
					"wait": map[string]any{
						"type":        "boolean",
						"description": "If true, wait for the subagent to complete. Defaults to true.",
					},
				},
				"required": []string{"type", "task"},
			},
		},
	}
}

// spawnAgentArgs are the arguments for spawn_agent.
type spawnAgentArgs struct {
	Type    string `json:"type"`
	Task    string `json:"task"`
	Context string `json:"context,omitempty"`
	Wait    *bool  `json:"wait,omitempty"`
}

// Run executes the tool.
func (t *SpawnAgentTool) Run(ctx context.Context, args json.RawMessage) string {
	var a spawnAgentArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	if t.manager == nil {
		return "Error: subagent manager not configured"
	}

	// Default to waiting
	wait := true
	if a.Wait != nil {
		wait = *a.Wait
	}

	if wait {
		// Synchronous: wait for result
		result, err := t.manager.SpawnSync(ctx, t.parentID, a.Type, a.Task, a.Context)
		if err != nil {
			return fmt.Sprintf("Subagent error: %v", err)
		}
		return result
	}

	// Async: spawn and return task ID
	taskID, err := t.manager.Spawn(ctx, t.parentID, a.Type, a.Task, a.Context)
	if err != nil {
		return fmt.Sprintf("Error spawning subagent: %v", err)
	}

	return fmt.Sprintf("Subagent spawned with ID: %s\nUse check_agent tool to get results when ready.", taskID)
}

// ============================================================================
// CheckAgentTool - Check status of a spawned subagent
// ============================================================================

// CheckAgentTool checks the status of a spawned subagent.
type CheckAgentTool struct {
	manager *bus.SubagentManager
}

// NewCheckAgentTool creates a new check agent tool.
func NewCheckAgentTool(manager *bus.SubagentManager) *CheckAgentTool {
	return &CheckAgentTool{manager: manager}
}

// Def returns the tool definition.
func (t *CheckAgentTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "check_agent",
			Description: "Check the status of a spawned subagent and get its result if completed.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "The task ID returned by spawn_agent.",
					},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

// checkAgentArgs are the arguments for check_agent.
type checkAgentArgs struct {
	TaskID string `json:"task_id"`
}

// Run executes the tool.
func (t *CheckAgentTool) Run(ctx context.Context, args json.RawMessage) string {
	var a checkAgentArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("Error: invalid arguments: %v", err)
	}

	if t.manager == nil {
		return "Error: subagent manager not configured"
	}

	task, ok := t.manager.GetTask(a.TaskID)
	if !ok {
		return fmt.Sprintf("Error: task not found: %s", a.TaskID)
	}

	switch task.Status {
	case bus.SubagentStatusPending:
		return fmt.Sprintf("Status: pending\nTask: %s", task.Task)
	case bus.SubagentStatusRunning:
		return fmt.Sprintf("Status: running\nTask: %s\nStarted: %s", task.Task, task.StartedAt.Format("15:04:05"))
	case bus.SubagentStatusCompleted:
		return fmt.Sprintf("Status: completed\nResult:\n%s", task.Result)
	case bus.SubagentStatusFailed:
		return fmt.Sprintf("Status: failed\nError: %s", task.Error)
	case bus.SubagentStatusCancelled:
		return "Status: cancelled"
	default:
		return fmt.Sprintf("Status: unknown (%s)", task.Status)
	}
}
