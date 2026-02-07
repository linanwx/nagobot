package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Snapshot is a runtime health snapshot of the current process.
type Snapshot struct {
	Status        string         `json:"status"`
	Goroutines    int            `json:"goroutines"`
	Memory        MemoryInfo     `json:"memory"`
	Runtime       RuntimeInfo    `json:"runtime"`
	Timestamp     string         `json:"timestamp"`
	Paths         *PathsInfo     `json:"paths,omitempty"`
	Thread        *ThreadInfo    `json:"thread,omitempty"`
	Session       *SessionInfo   `json:"session,omitempty"`
	WorkspaceTree *WorkspaceTree `json:"workspaceTree,omitempty"`
}

// MemoryInfo contains memory statistics in MB.
type MemoryInfo struct {
	AllocMB      float64 `json:"allocMB"`
	TotalAllocMB float64 `json:"totalAllocMB"`
	SysMB        float64 `json:"sysMB"`
	NumGC        uint32  `json:"numGC"`
}

// RuntimeInfo contains Go runtime metadata.
type RuntimeInfo struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	CPUs    int    `json:"cpus"`
}

// Options controls optional health details.
type Options struct {
	Workspace    string
	SessionsRoot string
	SkillsRoot   string

	ThreadID    string
	ThreadType  string
	SessionKey  string
	SessionFile string

	IncludeTree    bool
	TreeDepth      int
	TreeMaxEntries int
}

func (o Options) normalize() Options {
	o.Workspace = strings.TrimSpace(o.Workspace)
	o.SessionsRoot = strings.TrimSpace(o.SessionsRoot)
	o.SkillsRoot = strings.TrimSpace(o.SkillsRoot)
	o.ThreadID = strings.TrimSpace(o.ThreadID)
	o.ThreadType = strings.TrimSpace(o.ThreadType)
	o.SessionKey = strings.TrimSpace(o.SessionKey)
	o.SessionFile = strings.TrimSpace(o.SessionFile)
	return o
}

// PathsInfo contains important workspace paths.
type PathsInfo struct {
	Workspace    string `json:"workspace,omitempty"`
	SessionsRoot string `json:"sessionsRoot,omitempty"`
	SkillsRoot   string `json:"skillsRoot,omitempty"`
}

// ThreadInfo contains current thread metadata.
type ThreadInfo struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type,omitempty"`
	SessionKey  string `json:"sessionKey,omitempty"`
	SessionFile string `json:"sessionFile,omitempty"`
}

// SessionInfo contains quick session file diagnostics.
type SessionInfo struct {
	Path          string `json:"path,omitempty"`
	Exists        bool   `json:"exists"`
	FileSizeBytes int64  `json:"fileSizeBytes,omitempty"`
	MessagesCount int    `json:"messagesCount,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	ParseError    string `json:"parseError,omitempty"`
}

// WorkspaceTree contains a bounded tree snapshot for workspace diagnostics.
type WorkspaceTree struct {
	Root       string      `json:"root"`
	Depth      int         `json:"depth"`
	MaxEntries int         `json:"maxEntries"`
	Truncated  bool        `json:"truncated"`
	Error      string      `json:"error,omitempty"`
	Entries    []TreeEntry `json:"entries"`
}

// TreeEntry is one file or directory in the tree snapshot.
type TreeEntry struct {
	Path      string `json:"path"`
	Type      string `json:"type"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

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

// FormatText formats a snapshot into a human-readable text block.
func FormatText(s Snapshot) string {
	var b strings.Builder
	b.WriteString("nagobot Health\n")
	b.WriteString("==============\n\n")
	b.WriteString(fmt.Sprintf("Status: %s\n\n", s.Status))
	b.WriteString("Memory:\n")
	b.WriteString(fmt.Sprintf("  Allocated: %.2f MB\n", s.Memory.AllocMB))
	b.WriteString(fmt.Sprintf("  Total Allocated: %.2f MB\n", s.Memory.TotalAllocMB))
	b.WriteString(fmt.Sprintf("  System: %.2f MB\n", s.Memory.SysMB))
	b.WriteString(fmt.Sprintf("  GC Cycles: %d\n\n", s.Memory.NumGC))
	b.WriteString("Runtime:\n")
	b.WriteString(fmt.Sprintf("  Go Version: %s\n", s.Runtime.Version))
	b.WriteString(fmt.Sprintf("  OS/Arch: %s/%s\n", s.Runtime.OS, s.Runtime.Arch))
	b.WriteString(fmt.Sprintf("  CPUs: %d\n", s.Runtime.CPUs))
	b.WriteString(fmt.Sprintf("  Goroutines: %d\n", s.Goroutines))

	if s.Paths != nil {
		b.WriteString("\nPaths:\n")
		if s.Paths.Workspace != "" {
			b.WriteString(fmt.Sprintf("  Workspace: %s\n", s.Paths.Workspace))
		}
		if s.Paths.SessionsRoot != "" {
			b.WriteString(fmt.Sprintf("  Sessions: %s\n", s.Paths.SessionsRoot))
		}
		if s.Paths.SkillsRoot != "" {
			b.WriteString(fmt.Sprintf("  Skills: %s\n", s.Paths.SkillsRoot))
		}
	}

	if s.Thread != nil {
		b.WriteString("\nThread:\n")
		if s.Thread.ID != "" {
			b.WriteString(fmt.Sprintf("  ID: %s\n", s.Thread.ID))
		}
		if s.Thread.Type != "" {
			b.WriteString(fmt.Sprintf("  Type: %s\n", s.Thread.Type))
		}
		if s.Thread.SessionKey != "" {
			b.WriteString(fmt.Sprintf("  Session Key: %s\n", s.Thread.SessionKey))
		}
		if s.Thread.SessionFile != "" {
			b.WriteString(fmt.Sprintf("  Session File: %s\n", s.Thread.SessionFile))
		}
	}

	if s.Session != nil {
		b.WriteString("\nSession:\n")
		b.WriteString(fmt.Sprintf("  Exists: %t\n", s.Session.Exists))
		if s.Session.Path != "" {
			b.WriteString(fmt.Sprintf("  Path: %s\n", s.Session.Path))
		}
		if s.Session.FileSizeBytes > 0 {
			b.WriteString(fmt.Sprintf("  Size: %d bytes\n", s.Session.FileSizeBytes))
		}
		if s.Session.MessagesCount > 0 {
			b.WriteString(fmt.Sprintf("  Messages: %d\n", s.Session.MessagesCount))
		}
		if s.Session.UpdatedAt != "" {
			b.WriteString(fmt.Sprintf("  Updated At: %s\n", s.Session.UpdatedAt))
		}
		if s.Session.ParseError != "" {
			b.WriteString(fmt.Sprintf("  Parse Error: %s\n", s.Session.ParseError))
		}
	}

	if s.WorkspaceTree != nil {
		b.WriteString("\nWorkspace Tree:\n")
		b.WriteString(fmt.Sprintf("  Root: %s\n", s.WorkspaceTree.Root))
		b.WriteString(fmt.Sprintf("  Depth: %d\n", s.WorkspaceTree.Depth))
		b.WriteString(fmt.Sprintf("  Max Entries: %d\n", s.WorkspaceTree.MaxEntries))
		b.WriteString(fmt.Sprintf("  Truncated: %t\n", s.WorkspaceTree.Truncated))
		if s.WorkspaceTree.Error != "" {
			b.WriteString(fmt.Sprintf("  Error: %s\n", s.WorkspaceTree.Error))
		}
		b.WriteString("  Entries:\n")
		for _, entry := range s.WorkspaceTree.Entries {
			if entry.Type == "file" && entry.SizeBytes > 0 {
				b.WriteString(fmt.Sprintf("    - %s (%s, %d bytes)\n", entry.Path, entry.Type, entry.SizeBytes))
				continue
			}
			b.WriteString(fmt.Sprintf("    - %s (%s)\n", entry.Path, entry.Type))
		}
	}

	return b.String()
}

func inspectSessionFile(path string) *SessionInfo {
	info := &SessionInfo{Path: path}

	stat, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			info.Exists = false
			return info
		}
		info.ParseError = err.Error()
		return info
	}

	info.Exists = true
	info.FileSizeBytes = stat.Size()
	info.UpdatedAt = stat.ModTime().Format(time.RFC3339)

	data, err := os.ReadFile(path)
	if err != nil {
		info.ParseError = err.Error()
		return info
	}

	var payload struct {
		Messages  []json.RawMessage `json:"messages"`
		UpdatedAt string            `json:"updated_at"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		info.ParseError = err.Error()
		return info
	}

	info.MessagesCount = len(payload.Messages)
	if strings.TrimSpace(payload.UpdatedAt) != "" {
		info.UpdatedAt = strings.TrimSpace(payload.UpdatedAt)
	}
	return info
}

func buildWorkspaceTree(root string, depth, maxEntries int) *WorkspaceTree {
	if depth <= 0 {
		depth = 2
	}
	if maxEntries <= 0 {
		maxEntries = 200
	}

	tree := &WorkspaceTree{
		Root:       root,
		Depth:      depth,
		MaxEntries: maxEntries,
		Entries:    []TreeEntry{},
	}

	stat, err := os.Stat(root)
	if err != nil {
		tree.Error = err.Error()
		return tree
	}
	if !stat.IsDir() {
		tree.Error = "workspace is not a directory"
		return tree
	}

	var walk func(absDir, relDir string, level int)
	walk = func(absDir, relDir string, level int) {
		if tree.Truncated {
			return
		}

		dirEntries, readErr := os.ReadDir(absDir)
		if readErr != nil {
			return
		}
		sort.Slice(dirEntries, func(i, j int) bool {
			return dirEntries[i].Name() < dirEntries[j].Name()
		})

		for _, de := range dirEntries {
			if tree.Truncated {
				return
			}

			name := de.Name()
			if de.IsDir() && shouldSkipDir(name) {
				continue
			}

			relPath := name
			if relDir != "" {
				relPath = filepath.Join(relDir, name)
			}
			relPath = filepath.ToSlash(relPath)

			entry := TreeEntry{
				Path: relPath,
				Type: "file",
			}
			if de.IsDir() {
				entry.Type = "dir"
			} else if info, infoErr := de.Info(); infoErr == nil {
				entry.SizeBytes = info.Size()
			}

			tree.Entries = append(tree.Entries, entry)
			if len(tree.Entries) >= tree.MaxEntries {
				tree.Truncated = true
				return
			}

			if de.IsDir() && level+1 < depth {
				walk(filepath.Join(absDir, name), relPath, level+1)
			}
		}
	}

	walk(root, "", 0)
	return tree
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".tmp":
		return true
	default:
		return false
	}
}
