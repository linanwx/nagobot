package health

import (
	"fmt"
	"path/filepath"
	"runtime"
	"time"
)

// Collect returns a health snapshot for the current process.
func Collect(opts Options) Snapshot {
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
		opts.SessionKey != "" ||
		opts.SessionFile != "" {
		s.Thread = &ThreadInfo{
			ID:          opts.ThreadID,
			SessionKey:  opts.SessionKey,
			SessionFile: opts.SessionFile,
		}
	}

	if opts.SessionFile != "" {
		s.Session = inspectSessionFile(opts.SessionFile)
	}
	if opts.SessionsRoot != "" {
		s.Sessions = inspectSessionsRoot(opts.SessionsRoot)
	}
	if opts.Workspace != "" {
		s.Cron = inspectCronFile(filepath.Join(opts.Workspace, "cron.jsonl"))
	}

	if opts.IncludeTree && opts.Workspace != "" {
		s.WorkspaceTree = buildWorkspaceTree(opts.Workspace, opts.TreeDepth, opts.TreeMaxEntries)
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
