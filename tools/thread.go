package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/thread/msg"
)

// ThreadSpawner is implemented by thread.Thread to spawn child threads.
type ThreadSpawner interface {
	SpawnChild(ctx context.Context, agentName string, task string) (string, error)
}

// ThreadInfo is an alias for msg.ThreadInfo.
type ThreadInfo = msg.ThreadInfo

// ThreadChecker checks the status of threads.
type ThreadChecker interface {
	ThreadStatus(id string) (ThreadInfo, bool)
	ListThreads() []ThreadInfo
}

// SpawnThreadTool delegates a task to a child thread.
type SpawnThreadTool struct {
	spawner ThreadSpawner
}

// NewSpawnThreadTool creates a new spawn_thread tool.
func NewSpawnThreadTool(spawner ThreadSpawner) *SpawnThreadTool {
	return &SpawnThreadTool{spawner: spawner}
}

// Def returns the tool definition.
func (t *SpawnThreadTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "spawn_thread",
			Description: "Spawn a child thread for a delegated task. Use this when you need to run a subtask, invoke a sub-agent, or orchestrate parallel work. The child thread uses the selected agent template and task text as its prompt, and has an independent context. Always asynchronous: returns a child ID immediately. The child will wake the parent thread with a message when done.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Optional template agent name from agents/*.md.",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Task description for the child thread, including specific instructions and task background context. Recommended length: 20-800 words.",
					},
				},
				"required": []string{"task"},
			},
		},
	}
}

type spawnThreadArgs struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
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

	childID, err := t.spawner.SpawnChild(ctx, strings.TrimSpace(a.Agent), a.Task)
	if err != nil {
		return fmt.Sprintf("Error spawning thread: %v", err)
	}

	return fmt.Sprintf("Thread spawned with ID: %s\nThe child will wake this thread with a 'child_completed' message when done.", childID)
}

// CheckThreadTool checks the status of a spawned thread.
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
			Description: "Check the status of a spawned child thread by its ID. Returns thread metadata and execution status.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thread_id": map[string]any{
						"type":        "string",
						"description": "The thread ID, for example one returned by spawn_thread or any other thread ID.",
					},
				},
				"required": []string{"thread_id"},
			},
		},
	}
}

type checkThreadArgs struct {
	ThreadID string `json:"thread_id"`
}

// Run executes the tool.
func (t *CheckThreadTool) Run(_ context.Context, args json.RawMessage) string {
	var a checkThreadArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if t.checker == nil {
		return "Error: thread checker not configured"
	}

	id := strings.TrimSpace(a.ThreadID)
	if id == "" {
		return "Error: thread_id is required"
	}

	info, found := t.checker.ThreadStatus(id)
	if !found {
		return fmt.Sprintf("Error: thread %q not found", id)
	}

	result, _ := json.Marshal(info)
	return string(result)
}
