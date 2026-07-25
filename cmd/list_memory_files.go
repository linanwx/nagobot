package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/thread"
	"github.com/spf13/cobra"
)

const maxMemoryFiles = 3

var listMemoryFilesSession string

var listMemoryFilesCmd = &cobra.Command{
	Use:   "list-memory-files",
	Short: "List memory files that still need a summary",
	Long: `List memory files (compression summaries) that have no "summary" frontmatter yet,
newest first, at most ` + fmt.Sprint(maxMemoryFiles) + ` per call. Today's file is skipped — it may still grow.

Pass --session to scope the scan to one session; the nightly dream uses this to
summarize its own backlog.`,
	GroupID: "internal",
	RunE:    runListMemoryFiles,
}

func init() {
	listMemoryFilesCmd.Flags().StringVar(&listMemoryFilesSession, "session", "", "Only list files belonging to this session key")
	rootCmd.AddCommand(listMemoryFilesCmd)
}

type memoryFileEntry struct {
	SessionKey string `json:"session_key"`
	FilePath   string `json:"file_path"`
	Date       string `json:"date"`
	Size       int64  `json:"size"`
}

type listMemoryFilesOutput struct {
	Files   []memoryFileEntry `json:"files"`
	Scanned int               `json:"scanned"`
	Shown   int               `json:"shown"`
}

func runListMemoryFiles(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return fmt.Errorf("failed to get sessions dir: %w", err)
	}

	// No date cutoff. The old 30-day window existed only to bound the global
	// nightly cron sweep; that cron is gone, and the window silently made every
	// file older than it permanently unreachable — a memory file with no summary
	// is invisible to memory_index, so the backlog could never be drained.
	today := time.Now().Format("2006-01-02")
	wantSession := strings.TrimSpace(listMemoryFilesSession)

	var candidates []memoryFileEntry
	scanned := 0

	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		dir := filepath.Dir(path)
		if filepath.Base(dir) != "memory" || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		sessionDir := filepath.Dir(dir)
		key := deriveSessionKey(sessionsDir, filepath.Join(sessionDir, session.SessionFileName))
		if wantSession != "" && key != wantSession {
			return nil
		}

		scanned++
		date := strings.TrimSuffix(d.Name(), ".md")

		// Today's file may still be appended to by a later compression.
		if date == today {
			return nil
		}
		if hasMemorySummary(path) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		candidates = append(candidates, memoryFileEntry{
			SessionKey: key,
			FilePath:   path,
			Date:       date,
			Size:       info.Size(),
		})
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Date > candidates[j].Date
	})
	if len(candidates) > maxMemoryFiles {
		candidates = candidates[:maxMemoryFiles]
	}

	output := listMemoryFilesOutput{
		Files:   candidates,
		Scanned: scanned,
		Shown:   len(candidates),
	}
	if output.Files == nil {
		output.Files = []memoryFileEntry{}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// hasMemorySummary checks if a memory file has a YAML frontmatter with a "summary" field.
func hasMemorySummary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return false
	}
	yamlBlock, _, ok := thread.SplitFrontmatter(string(buf[:n]))
	if !ok {
		return false
	}
	return thread.ExtractFrontmatterValue(yamlBlock, "summary") != ""
}
