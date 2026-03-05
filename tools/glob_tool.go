package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/provider"
)

// GlobTool finds files matching a glob pattern.
type GlobTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *GlobTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "glob",
			Description: "Find files matching a glob pattern. Supports ** for recursive directory matching (e.g. \"**/*.go\", \"cmd/**/*.md\"). Results are sorted by modification time (most recent first), limited to 200 entries.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern to match, e.g. \"**/*.go\", \"*.json\", \"cmd/**/*.md\".",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Starting directory for the search. Defaults to workspace root.",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

const globMaxResults = 200

// skipDirs are directories to skip during traversal.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	"vendor":       true,
	".tox":         true,
	".mypy_cache":  true,
	".pytest_cache": true,
}

type globEntry struct {
	relPath string
	modTime int64
}

// Run executes the tool.
func (t *GlobTool) Run(ctx context.Context, args json.RawMessage) string {
	var a globArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	if a.Pattern == "" {
		return "Error: pattern is required"
	}

	searchPath := t.workspace
	if a.Path != "" {
		searchPath = resolveToolPath(a.Path, t.workspace)
	}
	if searchPath == "" {
		searchPath = "."
	}

	logger.Debug("glob tool", "pattern", a.Pattern, "searchPath", searchPath)

	info, err := os.Stat(searchPath)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if !info.IsDir() {
		return fmt.Sprintf("Error: path is not a directory: %s", absOrOriginal(searchPath))
	}

	var entries []globEntry
	hasDoublestar := strings.Contains(a.Pattern, "**")

	err = filepath.WalkDir(searchPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(searchPath, path)
		if err != nil {
			return nil
		}

		matched := false
		if hasDoublestar {
			matched = matchDoublestar(a.Pattern, rel)
		} else {
			matched, _ = filepath.Match(a.Pattern, rel)
			if !matched {
				// Also try matching just the filename
				matched, _ = filepath.Match(a.Pattern, d.Name())
			}
		}

		if matched {
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, globEntry{relPath: rel, modTime: fi.ModTime().UnixNano()})
		}
		return nil
	})
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	if len(entries) == 0 {
		return fmt.Sprintf("No files found matching pattern: %s", a.Pattern)
	}

	// Sort by modification time descending (most recent first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime > entries[j].modTime
	})

	truncated := false
	if len(entries) > globMaxResults {
		entries = entries[:globMaxResults]
		truncated = true
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.relPath)
		sb.WriteByte('\n')
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n(truncated: showing %d of more results)", globMaxResults))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// matchDoublestar handles glob patterns containing **.
// Supports common forms: **/*.ext, dir/**/*.ext, **/name, dir/**/name
func matchDoublestar(pattern, path string) bool {
	// Split pattern by **
	parts := strings.Split(pattern, "**")
	if len(parts) != 2 {
		// Multiple ** — fallback to simple suffix match
		return matchSuffix(pattern, path)
	}

	prefix := strings.TrimSuffix(parts[0], string(filepath.Separator))
	suffix := strings.TrimPrefix(parts[1], string(filepath.Separator))

	// Check prefix: if non-empty, path must start with it
	if prefix != "" {
		if !strings.HasPrefix(path, prefix+string(filepath.Separator)) && path != prefix {
			return false
		}
		// Strip prefix from path for suffix matching
		path = strings.TrimPrefix(path, prefix+string(filepath.Separator))
	}

	// Check suffix: match against every possible tail of the path
	if suffix == "" {
		return true // **/ matches everything
	}

	// Try matching suffix against the filename and progressively longer tails
	pathParts := strings.Split(path, string(filepath.Separator))
	for i := range pathParts {
		tail := strings.Join(pathParts[i:], string(filepath.Separator))
		if matched, _ := filepath.Match(suffix, tail); matched {
			return true
		}
	}
	return false
}

// matchSuffix is a simple fallback: extract the last segment and match.
func matchSuffix(pattern, path string) bool {
	// Just match the filename against the last part of the pattern
	lastSep := strings.LastIndex(pattern, string(filepath.Separator))
	filePattern := pattern
	if lastSep >= 0 {
		filePattern = pattern[lastSep+1:]
	}
	matched, _ := filepath.Match(filePattern, filepath.Base(path))
	return matched
}
