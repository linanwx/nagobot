package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	cronsvc "github.com/linanwx/nagobot/cron"
	"github.com/linanwx/nagobot/tools"
	robfigcron "github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
)

var cronCmd = &cobra.Command{
	Use:     "cron",
	Short:   "Manage cron jobs",
	GroupID: "internal",
}

// --- set-cron ---

var setCronCmd = &cobra.Command{
	Use:   "set-cron",
	Short: "Create or update a recurring cron job",
	RunE:  runSetCron,
}

var (
	setCronID   string
	setCronExpr string
	setCronTask string
)

func init() {
	setCronCmd.Flags().StringVar(&setCronID, "id", "", "Unique job ID (required)")
	setCronCmd.Flags().StringVar(&setCronExpr, "expr", "", "Cron expression, 5-field (required)")
	setCronCmd.Flags().StringVar(&setCronTask, "task", "", "Task prompt for the job (required)")
	_ = setCronCmd.MarkFlagRequired("id")
	_ = setCronCmd.MarkFlagRequired("expr")
	_ = setCronCmd.MarkFlagRequired("task")
	addCommonJobFlags(setCronCmd)
	cronCmd.AddCommand(setCronCmd)
}

func runSetCron(_ *cobra.Command, _ []string) error {
	expr := strings.TrimSpace(setCronExpr)
	if _, err := robfigcron.ParseStandard(expr); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	job := cronsvc.Job{
		ID:   setCronID,
		Kind: cronsvc.JobKindCron,
		Expr: expr,
		Task: setCronTask,
	}
	if err := applyCommonJobFlags(&job); err != nil {
		return err
	}
	updated, err := upsertJob(job)
	if err != nil {
		return err
	}
	action := "created"
	if updated {
		action = "updated"
	}
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "cron set-cron"}, {"status", action},
		{"job_id", job.ID}, {"kind", "cron"}, {"schedule", job.Expr},
	}, ""))
	return nil
}

// --- set-at ---

var setAtCmd = &cobra.Command{
	Use:   "set-at",
	Short: "Create or update a one-time scheduled job",
	RunE:  runSetAt,
}

var (
	setAtID   string
	setAtTime string
	setAtTask string
)

func init() {
	setAtCmd.Flags().StringVar(&setAtID, "id", "", "Unique job ID (required)")
	setAtCmd.Flags().StringVar(&setAtTime, "at", "", "Execution time in RFC3339 (required)")
	setAtCmd.Flags().StringVar(&setAtTask, "task", "", "Task prompt for the job (required)")
	_ = setAtCmd.MarkFlagRequired("id")
	_ = setAtCmd.MarkFlagRequired("at")
	_ = setAtCmd.MarkFlagRequired("task")
	addCommonJobFlags(setAtCmd)
	cronCmd.AddCommand(setAtCmd)
}

func runSetAt(_ *cobra.Command, _ []string) error {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(setAtTime))
	if err != nil {
		return fmt.Errorf("invalid --at time %q: %w", setAtTime, err)
	}
	job := cronsvc.Job{
		ID:     setAtID,
		Kind:   cronsvc.JobKindAt,
		AtTime: &t,
		Task:   setAtTask,
	}
	if err := applyCommonJobFlags(&job); err != nil {
		return err
	}
	updated, err := upsertJob(job)
	if err != nil {
		return err
	}
	action := "created"
	if updated {
		action = "updated"
	}
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "cron set-at"}, {"status", action},
		{"job_id", job.ID}, {"kind", "at"}, {"time", job.AtTime.Format(time.RFC3339)},
	}, ""))
	return nil
}

// --- remove ---

var cronRemoveCmd = &cobra.Command{
	Use:   "remove <id> [id...]",
	Short: "Remove one or more cron jobs by ID",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runCronRemove,
}

func init() {
	cronCmd.AddCommand(cronRemoveCmd)
}

func runCronRemove(_ *cobra.Command, args []string) error {
	ids := make([]string, 0, len(args))
	for _, id := range args {
		ids = append(ids, strings.TrimSpace(id))
	}

	raw, err := rpcCall("cron.remove", cronRemoveParams{IDs: ids})
	if err != nil {
		return fmt.Errorf("cron write requires a running daemon: %w", err)
	}
	var resp cronRemoveResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse cron.remove response: %w", err)
	}

	removedCount := 0
	for _, ok := range resp.Removed {
		if ok {
			removedCount++
		}
	}

	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "cron remove"}, {"status", "ok"},
		{"removed", fmt.Sprintf("%d", removedCount)},
		{"requested", fmt.Sprintf("%d", len(ids))},
	}, "") + "\n")
	for _, id := range ids {
		if resp.Removed[id] {
			fmt.Printf("removed: %s\n", id)
		} else {
			fmt.Printf("not_found: %s\n", id)
		}
	}
	return nil
}

// --- list ---

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cron jobs",
	Args:  cobra.NoArgs,
	RunE:  runCronList,
}

func init() {
	cronCmd.AddCommand(cronListCmd)
}

func runCronList(_ *cobra.Command, _ []string) error {
	storePath, err := cronStorePath()
	if err != nil {
		return err
	}
	jobs, err := cronsvc.ReadJobs(storePath)
	if err != nil {
		return fmt.Errorf("failed to read cron store: %w", err)
	}
	if len(jobs) == 0 {
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "cron list"}, {"status", "ok"}, {"count", "0"},
		}, "No cron jobs.") + "\n")
		return nil
	}
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "cron list"}, {"status", "ok"}, {"count", fmt.Sprintf("%d", len(jobs))},
	}, "") + "\n")
	fmt.Printf("ID\tKIND\tSCHEDULE\tAGENT\tWAKE-SESSION\tDIRECT-WAKE\tTASK\n")
	for _, job := range jobs {
		schedule := job.Expr
		if job.Kind == cronsvc.JobKindAt {
			if job.AtTime != nil {
				schedule = job.AtTime.Format(time.RFC3339)
			}
		}
		directWake := ""
		if job.DirectWake {
			directWake = "true"
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n", job.ID, job.Kind, schedule, job.Agent, job.WakeSession, directWake, job.Task)
	}
	return nil
}

// --- register root ---

func init() {
	rootCmd.AddCommand(cronCmd)
}

// --- shared helpers ---

var (
	commonAgent       string
	commonWakeSession string
	commonDirectWake  bool
)

func addCommonJobFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&commonAgent, "agent", "", "Agent template name (independent mode only)")
	cmd.Flags().StringVar(&commonWakeSession, "wake-session", "", "Independent mode: delivery hint shown in wake's delivery label. Inject mode: required target session receiving the task injection.")
	cmd.Flags().BoolVar(&commonDirectWake, "direct-wake", false, "Switch to inject mode: inject --task directly into --wake-session without running a cron agent. Requires --wake-session; rejects --agent.")
}

func applyCommonJobFlags(job *cronsvc.Job) error {
	job.Agent = strings.TrimSpace(commonAgent)
	job.WakeSession = strings.TrimSpace(commonWakeSession)
	job.DirectWake = commonDirectWake
	if job.DirectWake {
		if job.Agent != "" {
			return fmt.Errorf("--agent cannot be used with --direct-wake (inject mode preserves target session's existing agent)")
		}
		if job.WakeSession == "" {
			return fmt.Errorf("--direct-wake requires --wake-session (target session to inject into)")
		}
	}
	return nil
}

func cronStorePath() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return "", fmt.Errorf("failed to get workspace: %w", err)
	}
	return filepath.Join(workspace, "system", "cron.jsonl"), nil
}

// RPC payloads shared between the cron CLI (client) and serve.go (handler).
type cronUpsertResponse struct {
	Updated bool `json:"updated"`
}

type cronRemoveParams struct {
	IDs []string `json:"ids"`
}

type cronRemoveResponse struct {
	Removed map[string]bool `json:"removed"`
}

// upsertJob sends the job to the running daemon, whose scheduler is the single
// writer of cron.jsonl. The CLI must NOT write the store file itself: a
// read-modify-write from a separate process races other writers, and on
// 2026-07-15 two dreams running set-at in the same second silently erased one
// job that way. Going through the daemon also schedules the job immediately
// instead of waiting for the next minute reload. Returns true if an existing
// job was updated.
func upsertJob(job cronsvc.Job) (updated bool, err error) {
	job = cronsvc.Normalize(job)
	ok, _ := cronsvc.ValidateStored(job, time.Now())
	if !ok {
		return false, fmt.Errorf("invalid job: check id, task, and schedule fields")
	}

	raw, err := rpcCall("cron.upsert", job)
	if err != nil {
		return false, fmt.Errorf("cron write requires a running daemon: %w", err)
	}
	var resp cronUpsertResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return false, fmt.Errorf("parse cron.upsert response: %w", err)
	}
	return resp.Updated, nil
}
