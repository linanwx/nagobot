package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/provider"
)

// CronManager manages persisted cron jobs.
type CronManager interface {
	Add(id, expr, task, agent string) error
	Remove(id string) error
	List() []*cronpkg.Job
}

// ManageCronTool allows the model to add/remove/list cron jobs.
type ManageCronTool struct {
	manager CronManager
}

// NewManageCronTool creates a new manage_cron tool.
func NewManageCronTool(manager CronManager) *ManageCronTool {
	return &ManageCronTool{manager: manager}
}

// Def returns the tool definition.
func (t *ManageCronTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "manage_cron",
			Description: "Manage scheduled cron jobs. Supports add, remove, and list operations.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"operation": map[string]any{
						"type":        "string",
						"enum":        []string{"add", "remove", "list"},
						"description": "The cron operation to perform.",
					},
					"id": map[string]any{
						"type":        "string",
						"description": "Cron job ID. Required for add/remove.",
					},
					"expr": map[string]any{
						"type":        "string",
						"description": "Cron expression. Required for add.",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Task prompt for the cron job. Required for add.",
					},
					"agent": map[string]any{
						"type":        "string",
						"description": "Optional agent template name from agents/*.md.",
					},
				},
				"required": []string{"operation"},
			},
		},
	}
}

type manageCronArgs struct {
	Operation string `json:"operation"`
	ID        string `json:"id,omitempty"`
	Expr      string `json:"expr,omitempty"`
	Task      string `json:"task,omitempty"`
	Agent     string `json:"agent,omitempty"`
}

// Run executes the tool.
func (t *ManageCronTool) Run(ctx context.Context, args json.RawMessage) string {
	var a manageCronArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if t.manager == nil {
		return "Error: cron manager not configured"
	}

	op := strings.ToLower(strings.TrimSpace(a.Operation))
	switch op {
	case "add":
		if strings.TrimSpace(a.ID) == "" {
			return "Error: id is required for add"
		}
		if strings.TrimSpace(a.Expr) == "" {
			return "Error: expr is required for add"
		}
		if strings.TrimSpace(a.Task) == "" {
			return "Error: task is required for add"
		}
		if err := t.manager.Add(a.ID, a.Expr, a.Task, a.Agent); err != nil {
			return fmt.Sprintf("Error: failed to add cron job: %v", err)
		}
		return fmt.Sprintf("Cron job added: %s (%s)", strings.TrimSpace(a.ID), strings.TrimSpace(a.Expr))

	case "remove":
		if strings.TrimSpace(a.ID) == "" {
			return "Error: id is required for remove"
		}
		if err := t.manager.Remove(a.ID); err != nil {
			return fmt.Sprintf("Error: failed to remove cron job: %v", err)
		}
		return fmt.Sprintf("Cron job removed: %s", strings.TrimSpace(a.ID))

	case "list":
		jobs := t.manager.List()
		if len(jobs) == 0 {
			return "(no cron jobs)"
		}

		var b strings.Builder
		b.WriteString("Cron jobs:\n")
		for _, job := range jobs {
			if job == nil {
				continue
			}
			state := "disabled"
			if job.Enabled {
				state = "enabled"
			}
			line := fmt.Sprintf("- %s | %s | %s", job.ID, job.Expr, state)
			if strings.TrimSpace(job.Agent) != "" {
				line += fmt.Sprintf(" | agent=%s", strings.TrimSpace(job.Agent))
			}
			b.WriteString(line + "\n")
			b.WriteString(fmt.Sprintf("  task: %s\n", job.Task))
		}
		return strings.TrimSpace(b.String())

	default:
		return "Error: operation must be one of add, remove, list"
	}
}
