package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/linanwx/nagobot/bus"
	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/provider"
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
			Description: "Spawn a subagent to handle a specific task. Use for delegating complex subtasks.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Optional agent name (corresponds to agents/*.md). If not specified, uses a generic subagent.",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "The task description for the subagent. Be specific about what you want it to do.",
					},
					"context": map[string]any{
						"type":        "string",
						"description": "Optional additional context for the task (e.g., relevant code, files, or background info).",
					},
					"wait": map[string]any{
						"type":        "boolean",
						"description": "If true, wait for the subagent to complete. Defaults to true.",
					},
				},
				"required": []string{"task"},
			},
		},
	}
}

// spawnAgentArgs are the arguments for spawn_agent.
type spawnAgentArgs struct {
	Agent   string `json:"agent"`
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

	// Extract origin from context for async push delivery + session isolation
	var origin bus.SubagentOrigin
	if o, ok := channel.GetOrigin(ctx); ok {
		origin = bus.SubagentOrigin{
			Channel:    o.Channel,
			ReplyTo:    o.ReplyTo,
			SessionKey: o.SessionKey,
		}
	}

	if wait {
		// Synchronous: wait for result (origin not needed, result returned inline)
		result, err := t.manager.SpawnSync(ctx, t.parentID, a.Task, a.Context, a.Agent)
		if err != nil {
			return fmt.Sprintf("Subagent error: %v", err)
		}
		return result
	}

	// Async: spawn with origin for push delivery
	taskID, err := t.manager.SpawnWithOrigin(ctx, t.parentID, a.Task, a.Context, a.Agent, origin)
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
