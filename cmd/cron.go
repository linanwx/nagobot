package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/cron"
)

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Manage cron jobs",
	Long:  "List, add, remove, enable, disable, and run cron jobs defined in workspace/cron.yaml.",
}

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cron jobs",
	RunE:  runCronList,
}

var cronAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a cron job",
	Long: `Add a new cron job to cron.yaml.

Examples:
  nagobot cron add --id daily-report --name "Daily Report" --expr "0 9 * * *" --message "Generate daily report"
  nagobot cron add --id reminder --name "Reminder" --every 3600000 --message "Check tasks" --deliver --channel telegram --to 123456
  nagobot cron add --id once --name "One-shot" --at "2025-01-15T09:00:00Z" --message "Do this once" --delete-after-run`,
	RunE: runCronAdd,
}

var cronRemoveCmd = &cobra.Command{
	Use:   "remove [id]",
	Short: "Remove a cron job by ID",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronRemove,
}

var cronEnableCmd = &cobra.Command{
	Use:   "enable [id]",
	Short: "Enable a cron job",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronEnable,
}

var cronDisableCmd = &cobra.Command{
	Use:   "disable [id]",
	Short: "Disable a cron job",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronDisable,
}

var cronRunCmd = &cobra.Command{
	Use:   "run [id]",
	Short: "Run a cron job immediately",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronRun,
}

var (
	cronAddID             string
	cronAddName           string
	cronAddExpr           string
	cronAddEvery          int64
	cronAddAt             string
	cronAddMessage        string
	cronAddDeliver        bool
	cronAddChannel        string
	cronAddTo             string
	cronAddDeleteAfterRun bool
)

func init() {
	rootCmd.AddCommand(cronCmd)
	cronCmd.AddCommand(cronListCmd)
	cronCmd.AddCommand(cronAddCmd)
	cronCmd.AddCommand(cronRemoveCmd)
	cronCmd.AddCommand(cronEnableCmd)
	cronCmd.AddCommand(cronDisableCmd)
	cronCmd.AddCommand(cronRunCmd)

	cronAddCmd.Flags().StringVar(&cronAddID, "id", "", "Job ID (required)")
	cronAddCmd.Flags().StringVar(&cronAddName, "name", "", "Job name")
	cronAddCmd.Flags().StringVar(&cronAddExpr, "expr", "", "Cron expression (e.g., '0 9 * * *')")
	cronAddCmd.Flags().Int64Var(&cronAddEvery, "every", 0, "Interval in milliseconds")
	cronAddCmd.Flags().StringVar(&cronAddAt, "at", "", "One-shot time (RFC3339 e.g., '2025-01-15T09:00:00Z' or unix ms)")
	cronAddCmd.Flags().StringVar(&cronAddMessage, "message", "", "Payload message (required)")
	cronAddCmd.Flags().BoolVar(&cronAddDeliver, "deliver", false, "Deliver to channel instead of running through agent")
	cronAddCmd.Flags().StringVar(&cronAddChannel, "channel", "", "Target channel for delivery")
	cronAddCmd.Flags().StringVar(&cronAddTo, "to", "", "Target recipient (e.g., chat ID)")
	cronAddCmd.Flags().BoolVar(&cronAddDeleteAfterRun, "delete-after-run", false, "Delete job after it runs once (useful with --at)")
	_ = cronAddCmd.MarkFlagRequired("id")
	_ = cronAddCmd.MarkFlagRequired("message")
}

func getCronPath() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(workspace, "cron.yaml"), nil
}

func runCronList(cmd *cobra.Command, args []string) error {
	cronPath, err := getCronPath()
	if err != nil {
		return err
	}
	jobs, err := cron.LoadConfig(cronPath)
	if err != nil {
		return fmt.Errorf("failed to load cron config: %w", err)
	}
	if len(jobs) == 0 {
		fmt.Println("No cron jobs configured.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tENABLED\tSCHEDULE\tMESSAGE")
	fmt.Fprintln(w, "--\t----\t-------\t--------\t-------")
	for _, job := range jobs {
		schedule := ""
		switch job.Schedule.Kind {
		case cron.ScheduleCron:
			schedule = job.Schedule.Expr
		case cron.ScheduleEvery:
			schedule = fmt.Sprintf("every %dms", job.Schedule.EveryMs)
		case cron.ScheduleAt:
			schedule = fmt.Sprintf("at %d", job.Schedule.AtMs)
		}
		msg := job.Payload.Message
		if len(msg) > 40 {
			msg = msg[:40] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\t%s\n", job.ID, job.Name, job.Enabled, schedule, msg)
	}
	w.Flush()
	return nil
}

func runCronAdd(cmd *cobra.Command, args []string) error {
	cronPath, err := getCronPath()
	if err != nil {
		return err
	}
	jobs, _ := cron.LoadConfig(cronPath)

	// Count how many schedule types are specified
	specCount := 0
	if cronAddExpr != "" {
		specCount++
	}
	if cronAddEvery > 0 {
		specCount++
	}
	if cronAddAt != "" {
		specCount++
	}
	if specCount != 1 {
		return fmt.Errorf("must specify exactly one of --expr, --every, or --at")
	}

	var schedule cron.Schedule
	switch {
	case cronAddExpr != "":
		schedule = cron.Schedule{Kind: cron.ScheduleCron, Expr: cronAddExpr}
	case cronAddEvery > 0:
		schedule = cron.Schedule{Kind: cron.ScheduleEvery, EveryMs: cronAddEvery}
	case cronAddAt != "":
		// Try RFC3339 first, then unix ms
		atMs, err := parseAtTime(cronAddAt)
		if err != nil {
			return fmt.Errorf("invalid --at value: %w", err)
		}
		schedule = cron.Schedule{Kind: cron.ScheduleAt, AtMs: atMs}
	}

	job := cron.Job{
		ID:             cronAddID,
		Name:           cronAddName,
		Enabled:        true,
		Schedule:       schedule,
		DeleteAfterRun: cronAddDeleteAfterRun,
		Payload: cron.Payload{
			Message: cronAddMessage,
			Deliver: cronAddDeliver,
			Channel: cronAddChannel,
			To:      cronAddTo,
		},
	}

	jobs = append(jobs, job)
	if err := cron.SaveConfig(cronPath, jobs); err != nil {
		return err
	}
	fmt.Printf("Job '%s' added.\n", cronAddID)
	return nil
}

// parseAtTime parses an --at value as RFC3339 or unix milliseconds.
func parseAtTime(s string) (int64, error) {
	// Try RFC3339
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t.UnixMilli(), nil
	}
	// Try unix ms
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("expected RFC3339 (e.g., '2025-01-15T09:00:00Z') or unix milliseconds")
	}
	return ms, nil
}

func runCronRemove(cmd *cobra.Command, args []string) error {
	cronPath, err := getCronPath()
	if err != nil {
		return err
	}
	jobs, err := cron.LoadConfig(cronPath)
	if err != nil {
		return err
	}

	id := args[0]
	found := false
	var filtered []cron.Job
	for _, job := range jobs {
		if job.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, job)
	}
	if !found {
		return fmt.Errorf("job not found: %s", id)
	}

	if err := cron.SaveConfig(cronPath, filtered); err != nil {
		return err
	}
	fmt.Printf("Job '%s' removed.\n", id)
	return nil
}

func runCronEnable(cmd *cobra.Command, args []string) error {
	return setCronEnabled(args[0], true)
}

func runCronDisable(cmd *cobra.Command, args []string) error {
	return setCronEnabled(args[0], false)
}

func setCronEnabled(id string, enabled bool) error {
	cronPath, err := getCronPath()
	if err != nil {
		return err
	}
	jobs, err := cron.LoadConfig(cronPath)
	if err != nil {
		return err
	}

	found := false
	for i := range jobs {
		if jobs[i].ID == id {
			jobs[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("job not found: %s", id)
	}

	if err := cron.SaveConfig(cronPath, jobs); err != nil {
		return err
	}
	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	fmt.Printf("Job '%s' %s.\n", id, state)
	return nil
}

func runCronRun(cmd *cobra.Command, args []string) error {
	cronPath, err := getCronPath()
	if err != nil {
		return err
	}
	jobs, err := cron.LoadConfig(cronPath)
	if err != nil {
		return err
	}

	id := args[0]
	var target *cron.Job
	for i := range jobs {
		if jobs[i].ID == id {
			target = &jobs[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("job not found: %s", id)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	applyAgentOverrides(cfg)

	ag, err := agent.NewAgent(cfg)
	if err != nil {
		return err
	}
	defer ag.Close()

	fmt.Printf("Running job '%s': %s\n", target.ID, target.Payload.Message)
	response, err := ag.Run(context.Background(), target.Payload.Message)
	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}
	fmt.Println(response)
	return nil
}
