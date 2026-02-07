package health

import "strings"

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
	Cron          *CronInfo      `json:"cron,omitempty"`
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

// CronInfo contains cron file diagnostics and parsed jobs.
type CronInfo struct {
	Path          string        `json:"path,omitempty"`
	Exists        bool          `json:"exists"`
	FileSizeBytes int64         `json:"fileSizeBytes,omitempty"`
	JobsCount     int           `json:"jobsCount,omitempty"`
	UpdatedAt     string        `json:"updatedAt,omitempty"`
	ParseError    string        `json:"parseError,omitempty"`
	Jobs          []CronJobInfo `json:"jobs,omitempty"`
}

// CronJobInfo is a compact cron job summary for health output.
type CronJobInfo struct {
	ID                string `json:"id,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Expr              string `json:"expr,omitempty"`
	AtTime            string `json:"atTime,omitempty"`
	Agent             string `json:"agent,omitempty"`
	CreatorSessionKey string `json:"creatorSessionKey,omitempty"`
	Enabled           bool   `json:"enabled"`
	Silent            bool   `json:"silent"`
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
