package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	cronsvc "github.com/linanwx/nagobot/cron"
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
	applyCommonJobFlags(&job)
	updated, err := upsertJob(job)
	if err != nil {
		return err
	}
	action := "created"
	if updated {
		action = "updated"
	}
	fmt.Printf("Job %s: %s (kind=cron, expr=%s)\n", action, job.ID, job.Expr)
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
		AtTime: t,
		Task:   setAtTask,
	}
	applyCommonJobFlags(&job)
	updated, err := upsertJob(job)
	if err != nil {
		return err
	}
	action := "created"
	if updated {
		action = "updated"
	}
	fmt.Printf("Job %s: %s (kind=at, at=%s)\n", action, job.ID, job.AtTime.Format(time.RFC3339))
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
	storePath, err := cronStorePath()
	if err != nil {
		return err
	}
	jobs, err := cronsvc.ReadJobs(storePath)
	if err != nil {
		return fmt.Errorf("failed to read cron store: %w", err)
	}

	removeSet := make(map[string]bool, len(args))
	for _, id := range args {
		removeSet[strings.TrimSpace(id)] = true
	}

	var kept []cronsvc.Job
	removed := make(map[string]bool)
	for _, job := range jobs {
		if removeSet[job.ID] {
			removed[job.ID] = true
		} else {
			kept = append(kept, job)
		}
	}

	if len(removed) > 0 {
		if err := cronsvc.WriteJobs(storePath, kept); err != nil {
			return fmt.Errorf("failed to write cron store: %w", err)
		}
	}

	for _, id := range args {
		id = strings.TrimSpace(id)
		if removed[id] {
			fmt.Printf("Removed: %s\n", id)
		} else {
			fmt.Printf("Not found: %s\n", id)
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
		fmt.Println("No cron jobs.")
		return nil
	}
	for _, job := range jobs {
		schedule := job.Expr
		if job.Kind == cronsvc.JobKindAt {
			schedule = job.AtTime.Format(time.RFC3339)
		}
		task := job.Task
		if len(task) > 50 {
			task = task[:50] + "..."
		}
		fmt.Printf("%-20s %-5s %-25s %-10s %s\n", job.ID, job.Kind, schedule, job.Agent, task)
	}
	return nil
}

// --- register root ---

func init() {
	rootCmd.AddCommand(cronCmd)
}

// --- shared helpers ---

var (
	commonAgent             string
	commonCreatorSessionKey string
	commonSilent            bool
)

func addCommonJobFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&commonAgent, "agent", "", "Agent template name")
	cmd.Flags().StringVar(&commonCreatorSessionKey, "creator-session-key", "", "Session key to wake on completion")
	cmd.Flags().BoolVar(&commonSilent, "silent", false, "Suppress result delivery")
}

func applyCommonJobFlags(job *cronsvc.Job) {
	job.Agent = strings.TrimSpace(commonAgent)
	job.CreatorSessionKey = strings.TrimSpace(commonCreatorSessionKey)
	job.Silent = commonSilent
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
	return filepath.Join(workspace, "cron.jsonl"), nil
}

// upsertJob writes a job to the store. Returns true if an existing job was updated.
func upsertJob(job cronsvc.Job) (updated bool, err error) {
	job = cronsvc.Normalize(job)
	ok, _ := cronsvc.ValidateStored(job, time.Now())
	if !ok {
		return false, fmt.Errorf("invalid job: check id, task, and schedule fields")
	}

	storePath, err := cronStorePath()
	if err != nil {
		return false, err
	}
	existing, err := cronsvc.ReadJobs(storePath)
	if err != nil {
		return false, fmt.Errorf("failed to read cron store: %w", err)
	}

	// Upsert: replace if same ID exists, otherwise append.
	for i, j := range existing {
		if j.ID == job.ID {
			existing[i] = job
			if err := cronsvc.WriteJobs(storePath, existing); err != nil {
				return false, fmt.Errorf("failed to write cron store: %w", err)
			}
			return true, nil
		}
	}

	existing = append(existing, job)
	if err := cronsvc.WriteJobs(storePath, existing); err != nil {
		return false, fmt.Errorf("failed to write cron store: %w", err)
	}
	return false, nil
}
