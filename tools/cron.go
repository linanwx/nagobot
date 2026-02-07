package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cronpkg "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/provider"
)

// CronManager manages persisted cron jobs.
type CronManager interface {
	Add(id, expr, task, agent, creatorSessionKey string, silent bool) error
	AddAt(id string, atTime time.Time, task, agent, creatorSessionKey string, silent bool) error
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
	now := time.Now()
	currentTime := now.Format(time.RFC3339)
	currentOffset := now.Format("-07:00")

	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "manage_cron",
			Description: fmt.Sprintf("Manage scheduled cron jobs. Supports add, remove, and list operations. Server current time: %s (UTC%s).", currentTime, currentOffset),
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
						"description": "Cron expression for recurring jobs. Required for add when at_time is not provided.",
					},
					"at_time": map[string]any{
						"type":        "string",
						"description": "One-time schedule in RFC3339 with explicit timezone offset (e.g. 2026-02-07T15:04:05+08:00).",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "Task prompt for the cron job. Required for add.",
					},
					"silent": map[string]any{
						"type":        "boolean",
						"description": "Optional. true = run silently without waking the creator thread. Default is false.",
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
	AtTime    string `json:"at_time,omitempty"`
	Task      string `json:"task,omitempty"`
	Silent    *bool  `json:"silent,omitempty"`
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
		id := strings.TrimSpace(a.ID)
		if id == "" {
			return "Error: id is required for add"
		}
		if strings.TrimSpace(a.Task) == "" {
			return "Error: task is required for add"
		}
		silent := a.Silent != nil && *a.Silent
		creatorSessionKey := RuntimeContextFrom(ctx).SessionKey
		serverOffset := time.Now().Format("-07:00")

		expr := strings.TrimSpace(a.Expr)
		hasOneTime := strings.TrimSpace(a.AtTime) != ""
		if expr == "" && !hasOneTime {
			return "Error: either expr or at_time is required for add"
		}
		if expr != "" && hasOneTime {
			return "Error: use either expr (recurring) or at_time (one-time), not both"
		}

		if expr != "" {
			if err := t.manager.Add(a.ID, expr, a.Task, a.Agent, creatorSessionKey, silent); err != nil {
				return fmt.Sprintf("Error: failed to add cron job: %v", err)
			}
			mode := "wake_creator"
			if silent {
				mode = "silent"
			}
			return fmt.Sprintf("Cron job added: %s (%s)\nmode: %s\ncreator_session_key: %s\nserver_timezone: UTC%s\nsame_timezone: n/a (expr has no explicit timezone)", id, expr, mode, creatorSessionKey, serverOffset)
		}

		runAt, err := parseOneTime(strings.TrimSpace(a.AtTime))
		if err != nil {
			return fmt.Sprintf("Error: invalid at_time: %v", err)
		}
		if err := t.manager.AddAt(a.ID, runAt, a.Task, a.Agent, creatorSessionKey, silent); err != nil {
			return fmt.Sprintf("Error: failed to add one-time job: %v", err)
		}
		inputOffset := runAt.Format("-07:00")
		mode := "wake_creator"
		if silent {
			mode = "silent"
		}
		return fmt.Sprintf(
			"One-time cron job added: %s (%s)\nmode: %s\ncreator_session_key: %s\nserver_timezone: UTC%s\ninput_timezone: UTC%s\nsame_timezone: %t",
			id,
			runAt.Format(time.RFC3339),
			mode,
			creatorSessionKey,
			serverOffset,
			inputOffset,
			inputOffset == serverOffset,
		)

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
			line := fmt.Sprintf("- %s | %s | %s", job.ID, formatSchedule(job), state)
			if job.Silent {
				line += " | mode=silent"
			} else {
				line += " | mode=wake_creator"
			}
			if strings.TrimSpace(job.Agent) != "" {
				line += fmt.Sprintf(" | agent=%s", strings.TrimSpace(job.Agent))
			}
			if strings.TrimSpace(job.CreatorSessionKey) != "" {
				line += fmt.Sprintf(" | creator=%s", strings.TrimSpace(job.CreatorSessionKey))
			}
			b.WriteString(line + "\n")
			b.WriteString(fmt.Sprintf("  task: %s\n", job.Task))
		}
		return strings.TrimSpace(b.String())

	default:
		return "Error: operation must be one of add, remove, list"
	}
}

func parseOneTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("at_time is required")
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 with timezone offset, e.g. 2026-02-07T15:04:05+08:00")
	}
	return t, nil
}

func formatSchedule(job *cronpkg.Job) string {
	if job == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(job.Kind)) {
	case cronpkg.JobKindAt:
		if !job.AtTime.IsZero() {
			return "at:" + job.AtTime.Format(time.RFC3339)
		}
		return "at"
	default:
		return strings.TrimSpace(job.Expr)
	}
}
