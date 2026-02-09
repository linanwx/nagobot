package health

import "strings"

// Snapshot is a runtime health snapshot of the current process.
type Snapshot struct {
	Status        string         `json:"status"`
	Provider      string         `json:"provider,omitempty"`
	Model         string         `json:"model,omitempty"`
	Goroutines    int            `json:"goroutines"`
	Memory        MemoryInfo     `json:"memory"`
	Runtime       RuntimeInfo    `json:"runtime"`
	Time          TimeInfo       `json:"time"`
	Timestamp     string         `json:"timestamp"`
	Paths         *PathsInfo     `json:"paths,omitempty"`
	Thread        *ThreadInfo    `json:"thread,omitempty"`
	Session       *SessionInfo   `json:"session,omitempty"`
	Sessions      *SessionsInfo  `json:"sessions,omitempty"`
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

// TimeInfo contains current process time and timezone diagnostics.
type TimeInfo struct {
	Local     string `json:"local"`
	UTC       string `json:"utc"`
	Weekday   string `json:"weekday"`
	Timezone  string `json:"timezone"`
	UTCOffset string `json:"utcOffset"`
	Unix      int64  `json:"unix"`
}

// Options controls optional health details.
type Options struct {
	Workspace    string
	SessionsRoot string
	SkillsRoot   string

	Provider string
	Model    string

	ThreadID    string
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

// SessionsInfo contains recursive diagnostics for sessions/*.json files.
type SessionsInfo struct {
	Root         string             `json:"root,omitempty"`
	Exists       bool               `json:"exists"`
	FilesCount   int                `json:"filesCount,omitempty"`
	ValidCount   int                `json:"validCount,omitempty"`
	InvalidCount int                `json:"invalidCount,omitempty"`
	InvalidFiles []SessionFileError `json:"invalidFiles,omitempty"`
	ScanError    string             `json:"scanError,omitempty"`
}

// SessionFileError captures one invalid session file parse result.
type SessionFileError struct {
	Path       string `json:"path"`
	ParseError string `json:"parseError"`
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
