package health

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"
)

// Collect returns a health snapshot for the current process.
func Collect(ctx context.Context, opts Options) Snapshot {
	opts = opts.normalize()
	now := time.Now()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	zoneName, zoneOffsetSeconds := now.Zone()

	s := Snapshot{
		Status:     "healthy",
		Provider:   opts.Provider,
		Model:      opts.Model,
		Goroutines: runtime.NumGoroutine(),
		Memory: MemoryInfo{
			AllocMB:      float64(mem.Alloc) / 1024 / 1024,
			TotalAllocMB: float64(mem.TotalAlloc) / 1024 / 1024,
			SysMB:        float64(mem.Sys) / 1024 / 1024,
			NumGC:        mem.NumGC,
		},
		Runtime: RuntimeInfo{
			Version: runtime.Version(),
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			CPUs:    runtime.NumCPU(),
		},
		Time: TimeInfo{
			Local:     now.Format(time.RFC3339),
			UTC:       now.UTC().Format(time.RFC3339),
			Weekday:   now.Weekday().String(),
			Timezone:  zoneName,
			UTCOffset: formatUTCOffset(zoneOffsetSeconds),
			Unix:      now.Unix(),
		},
		Timestamp: now.Format(time.RFC3339),
	}

	if opts.Workspace != "" || opts.SessionsRoot != "" || opts.SkillsRoot != "" {
		s.Paths = &PathsInfo{
			Workspace:    opts.Workspace,
			SessionsRoot: opts.SessionsRoot,
			SkillsRoot:   opts.SkillsRoot,
		}
	}

	if opts.ThreadID != "" ||
		opts.AgentName != "" ||
		opts.SessionKey != "" ||
		opts.SessionFile != "" {
		s.Thread = &ThreadInfo{
			ID:          opts.ThreadID,
			AgentName:   opts.AgentName,
			SessionKey:  opts.SessionKey,
			SessionFile: opts.SessionFile,
		}
	}

	if opts.SessionFile != "" && ctx.Err() == nil {
		s.Session = inspectSessionFile(opts.SessionFile)
	}
	if opts.SessionsRoot != "" && ctx.Err() == nil {
		s.Sessions = inspectSessionsRoot(ctx, opts.SessionsRoot)
	}
	if opts.Workspace != "" && ctx.Err() == nil {
		s.Cron = inspectCronFile(filepath.Join(opts.Workspace, "system", "cron.jsonl"))
	}

	if opts.Channels != nil {
		s.Channels = opts.Channels
	}

	if opts.IncludeTree && opts.Workspace != "" && ctx.Err() == nil {
		s.WorkspaceTree = buildWorkspaceTree(ctx, opts.Workspace, opts.TreeDepth, opts.TreeMaxEntries)
	}

	return s
}

func formatUTCOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
}
