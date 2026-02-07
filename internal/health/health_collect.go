package health

import (
	"runtime"
	"time"
)

// Collect returns a health snapshot for the current process.
func Collect(opts Options) Snapshot {
	opts = opts.normalize()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	s := Snapshot{
		Status:     "healthy",
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
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if opts.Workspace != "" || opts.SessionsRoot != "" || opts.SkillsRoot != "" {
		s.Paths = &PathsInfo{
			Workspace:    opts.Workspace,
			SessionsRoot: opts.SessionsRoot,
			SkillsRoot:   opts.SkillsRoot,
		}
	}

	if opts.ThreadID != "" ||
		opts.ThreadType != "" ||
		opts.SessionKey != "" ||
		opts.SessionFile != "" {
		s.Thread = &ThreadInfo{
			ID:          opts.ThreadID,
			Type:        opts.ThreadType,
			SessionKey:  opts.SessionKey,
			SessionFile: opts.SessionFile,
		}
	}

	if opts.SessionFile != "" {
		s.Session = inspectSessionFile(opts.SessionFile)
	}

	if opts.IncludeTree && opts.Workspace != "" {
		s.WorkspaceTree = buildWorkspaceTree(opts.Workspace, opts.TreeDepth, opts.TreeMaxEntries)
	}

	return s
}
