package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/provider"
)

// ThreadSpawner is implemented by thread.Thread to spawn child threads.
type ThreadSpawner interface {
	SpawnChild(ctx context.Context, ag *agent.Agent, task, taskContext string, wait bool) (string, error)
}

// ThreadChecker is implemented by thread.Thread to check child thread status.
type ThreadChecker interface {
	GetChild(childID string) (status, result string, err error)
}

// AgentResolver resolves named template agents.
type AgentResolver interface {
	Get(name string) *agent.Agent
}

// SpawnThreadTool delegates a task to a child thread.
type SpawnThreadTool struct {
	spawner ThreadSpawner
	agents  AgentResolver
}

// NewSpawnThreadTool creates a new spawn_thread tool.
func NewSpawnThreadTool(spawner ThreadSpawner, agents AgentResolver) *SpawnThreadTool {
	return &SpawnThreadTool{spawner: spawner, agents: agents}
}

// Def returns the tool definition.
func (t *SpawnThreadTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "spawn_thread",
			Description: "Spawn a child thread for a delegated task. Use wait=true for sync results, wait=false for async execution.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Optional template agent name from agents/*.md.",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Task description for the child thread.",
					},
					"context": map[string]any{
						"type":        "string",
						"description": "Optional extra context for the task.",
					},
					"wait": map[string]any{
						"type":        "boolean",
						"description": "Whether to wait for completion. Defaults to true.",
					},
				},
				"required": []string{"task"},
			},
		},
	}
}

type spawnThreadArgs struct {
	Agent   string `json:"agent"`
	Task    string `json:"task"`
	Context string `json:"context,omitempty"`
	Wait    *bool  `json:"wait,omitempty"`
}

// Run executes the tool.
func (t *SpawnThreadTool) Run(ctx context.Context, args json.RawMessage) string {
	var a spawnThreadArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if t.spawner == nil {
		return "Error: thread spawner not configured"
	}

	wait := true
	if a.Wait != nil {
		wait = *a.Wait
	}

	var childAgent *agent.Agent
	if strings.TrimSpace(a.Agent) != "" {
		if t.agents == nil {
			return "Error: agent registry not configured"
		}
		childAgent = t.agents.Get(strings.TrimSpace(a.Agent))
		if childAgent == nil {
			return fmt.Sprintf("Error: agent not found: %s", a.Agent)
		}
	}

	result, err := t.spawner.SpawnChild(ctx, childAgent, a.Task, a.Context, wait)
	if err != nil {
		return fmt.Sprintf("Error spawning thread: %v", err)
	}

	if wait {
		return result
	}

	return fmt.Sprintf("Thread spawned with ID: %s\nUse check_thread to inspect status.", result)
}

// CheckThreadTool checks async child thread status.
type CheckThreadTool struct {
	checker ThreadChecker
}

// NewCheckThreadTool creates a new check_thread tool.
func NewCheckThreadTool(checker ThreadChecker) *CheckThreadTool {
	return &CheckThreadTool{checker: checker}
}

// Def returns the tool definition.
func (t *CheckThreadTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "check_thread",
			Description: "Check status of an async child thread spawned with spawn_thread.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"child_id": map[string]any{
						"type":        "string",
						"description": "Child thread ID returned by spawn_thread.",
					},
				},
				"required": []string{"child_id"},
			},
		},
	}
}

type checkThreadArgs struct {
	ChildID string `json:"child_id"`
}

// Run executes the tool.
func (t *CheckThreadTool) Run(ctx context.Context, args json.RawMessage) string {
	var a checkThreadArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if t.checker == nil {
		return "Error: thread checker not configured"
	}

	status, result, err := t.checker.GetChild(a.ChildID)
	if err != nil {
		if status == "failed" {
			return fmt.Sprintf("Status: failed\nError: %v", err)
		}
		return fmt.Sprintf("Error: %v", err)
	}

	if status == "completed" {
		return fmt.Sprintf("Status: completed\nResult:\n%s", result)
	}
	return fmt.Sprintf("Status: %s", status)
}
