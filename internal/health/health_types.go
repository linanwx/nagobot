package health

import (
	"strings"

	"github.com/linanwx/nagobot/thread/msg"
)

// Snapshot is a runtime health snapshot of the current process.
type Snapshot struct {
	Status        string         `json:"status" yaml:"status"`
	Provider      string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model         string         `json:"model,omitempty" yaml:"model,omitempty"`
	Goroutines    int            `json:"goroutines" yaml:"goroutines"`
	Memory        MemoryInfo     `json:"memory" yaml:"memory"`
	Runtime       RuntimeInfo    `json:"runtime" yaml:"runtime"`
	Time          TimeInfo       `json:"time" yaml:"time"`
	Timestamp     string         `json:"timestamp" yaml:"timestamp"`
	Paths         *PathsInfo     `json:"paths,omitempty" yaml:"paths,omitempty"`
	Thread        *ThreadInfo    `json:"thread,omitempty" yaml:"thread,omitempty"`
	Session       *SessionInfo   `json:"session,omitempty" yaml:"session,omitempty"`
	Sessions      *SessionsInfo  `json:"sessions,omitempty" yaml:"sessions,omitempty"`
	Channels      *ChannelsInfo   `json:"channels,omitempty" yaml:"channels,omitempty"`
	Cron          *CronInfo      `json:"cron,omitempty" yaml:"cron,omitempty"`
	AllThreads []msg.ThreadInfo `json:"allThreads,omitempty" yaml:"all_threads,omitempty"`
	WorkspaceTree *WorkspaceTree  `json:"workspaceTree,omitempty" yaml:"workspace_tree,omitempty"`
}

// MemoryInfo contains memory statistics in MB.
type MemoryInfo struct {
	AllocMB      float64 `json:"allocMB" yaml:"alloc_mb"`
	TotalAllocMB float64 `json:"totalAllocMB" yaml:"total_alloc_mb"`
	SysMB        float64 `json:"sysMB" yaml:"sys_mb"`
	NumGC        uint32  `json:"numGC" yaml:"num_gc"`
}

// RuntimeInfo contains Go runtime metadata.
type RuntimeInfo struct {
	Version string `json:"version" yaml:"version"`
	OS      string `json:"os" yaml:"os"`
	Arch    string `json:"arch" yaml:"arch"`
	CPUs    int    `json:"cpus" yaml:"cpus"`
}

// TimeInfo contains current process time and timezone diagnostics.
type TimeInfo struct {
	Local     string `json:"local" yaml:"local"`
	UTC       string `json:"utc" yaml:"utc"`
	Weekday   string `json:"weekday" yaml:"weekday"`
	Timezone  string `json:"timezone" yaml:"timezone"`
	UTCOffset string `json:"utcOffset" yaml:"utc_offset"`
	Unix      int64  `json:"unix" yaml:"unix"`
}

// Options controls optional health details.
type Options struct {
	Workspace    string
	SessionsRoot string
	SkillsRoot   string

	Provider string
	Model    string

	ThreadID    string
	AgentName   string
	SessionKey  string
	SessionFile string

	Channels *ChannelsInfo

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
	Workspace    string `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	SessionsRoot string `json:"sessionsRoot,omitempty" yaml:"sessions_root,omitempty"`
	SkillsRoot   string `json:"skillsRoot,omitempty" yaml:"skills_root,omitempty"`
}

// ThreadInfo contains current thread metadata.
type ThreadInfo struct {
	ID          string `json:"id,omitempty" yaml:"id,omitempty"`
	AgentName   string `json:"agentName,omitempty" yaml:"agent_name,omitempty"`
	SessionKey  string `json:"sessionKey,omitempty" yaml:"session_key,omitempty"`
	SessionFile string `json:"sessionFile,omitempty" yaml:"session_file,omitempty"`
}

// SessionInfo contains quick session file diagnostics.
type SessionInfo struct {
	Path          string `json:"path,omitempty" yaml:"path,omitempty"`
	Exists        bool   `json:"exists" yaml:"exists"`
	FileSizeBytes int64  `json:"fileSizeBytes,omitempty" yaml:"file_size_bytes,omitempty"`
	MessagesCount int    `json:"messagesCount,omitempty" yaml:"messages_count,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty" yaml:"updated_at,omitempty"`
	ParseError    string `json:"parseError,omitempty" yaml:"parse_error,omitempty"`
}

// SessionsInfo contains recursive diagnostics for sessions/*.json files.
type SessionsInfo struct {
	Root         string             `json:"root,omitempty" yaml:"root,omitempty"`
	Exists       bool               `json:"exists" yaml:"exists"`
	FilesCount   int                `json:"filesCount,omitempty" yaml:"files_count,omitempty"`
	ValidCount   int                `json:"validCount,omitempty" yaml:"valid_count,omitempty"`
	InvalidCount int                `json:"invalidCount,omitempty" yaml:"invalid_count,omitempty"`
	InvalidFiles []SessionFileError `json:"invalidFiles,omitempty" yaml:"invalid_files,omitempty"`
	ScanError    string             `json:"scanError,omitempty" yaml:"scan_error,omitempty"`
}

// SessionFileError captures one invalid session file parse result.
type SessionFileError struct {
	Path       string `json:"path" yaml:"path"`
	ParseError string `json:"parseError" yaml:"parse_error"`
}

// CronInfo contains cron file diagnostics.
type CronInfo struct {
	Path          string `json:"path,omitempty" yaml:"path,omitempty"`
	Exists        bool   `json:"exists" yaml:"exists"`
	FileSizeBytes int64  `json:"fileSizeBytes,omitempty" yaml:"file_size_bytes,omitempty"`
	JobsCount     int    `json:"jobsCount,omitempty" yaml:"jobs_count,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty" yaml:"updated_at,omitempty"`
	ParseError    string `json:"parseError,omitempty" yaml:"parse_error,omitempty"`
}

// WorkspaceTree contains a bounded tree snapshot for workspace diagnostics.
type WorkspaceTree struct {
	Root       string      `json:"root" yaml:"root"`
	Depth      int         `json:"depth" yaml:"depth"`
	MaxEntries int         `json:"maxEntries" yaml:"max_entries"`
	Truncated  bool        `json:"truncated" yaml:"truncated"`
	Error      string      `json:"error,omitempty" yaml:"error,omitempty"`
	Entries    []TreeEntry `json:"entries" yaml:"entries"`
}

// TreeEntry is one file or directory in the tree snapshot.
type TreeEntry struct {
	Path      string `json:"path" yaml:"path"`
	Type      string `json:"type" yaml:"type"`
	SizeBytes int64  `json:"sizeBytes,omitempty" yaml:"size_bytes,omitempty"`
}

// ChannelsInfo contains active channel configuration for health output.
type ChannelsInfo struct {
	AdminUserID string            `json:"adminUserID,omitempty" yaml:"admin_user_id,omitempty"`
	UserAgents  map[string]string `json:"userAgents,omitempty" yaml:"user_agents,omitempty"`
	Telegram    *TelegramInfo     `json:"telegram,omitempty" yaml:"telegram,omitempty"`
	Web         *WebInfo          `json:"web,omitempty" yaml:"web,omitempty"`
}

// TelegramInfo contains Telegram channel config (token masked).
type TelegramInfo struct {
	Configured bool    `json:"configured" yaml:"configured"`
	AllowedIDs []int64 `json:"allowedIDs,omitempty" yaml:"allowed_ids,omitempty"`
}

// WebInfo contains Web channel config.
type WebInfo struct {
	Addr string `json:"addr,omitempty" yaml:"addr,omitempty"`
}
