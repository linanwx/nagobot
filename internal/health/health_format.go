package health

import (
	"fmt"
	"strings"
)

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
