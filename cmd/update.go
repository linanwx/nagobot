package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/cmd/service"
	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var updatePre bool

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update nagobot to the latest version",
	Long: `Check for the latest release on GitHub and update the binary in place.

By default only stable (non-pre-release) versions are considered.
Use --pre to include pre-release versions.

After replacing the binary the running service is automatically restarted.`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updatePre, "pre", false, "Include pre-release versions")
	rootCmd.AddCommand(updateCmd)
}

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

// fetchLatestVersion returns the tag name of the target release.
// When pre is false, it uses /releases/latest (stable only).
// When pre is true, it lists releases and picks the first non-draft entry.
func fetchLatestVersion(pre bool) (string, error) {
	if !pre {
		resp, err := http.Get("https://api.github.com/repos/linanwx/nagobot/releases/latest")
		if err != nil {
			return "", fmt.Errorf("cannot reach GitHub API: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("GitHub API returned %s", resp.Status)
		}

		var rel ghRelease
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return "", fmt.Errorf("cannot parse release info: %w", err)
		}
		if rel.TagName == "" {
			return "", fmt.Errorf("no stable release found")
		}
		return rel.TagName, nil
	}

	// --pre: list all releases and pick the first non-draft.
	resp, err := http.Get("https://api.github.com/repos/linanwx/nagobot/releases?per_page=10")
	if err != nil {
		return "", fmt.Errorf("cannot reach GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("cannot parse releases: %w", err)
	}

	for _, r := range releases {
		if !r.Draft {
			return r.TagName, nil
		}
	}
	return "", fmt.Errorf("no release found")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	fmt.Printf("Current version: %s\n", Version)

	latest, err := fetchLatestVersion(updatePre)
	if err != nil {
		return err
	}

	if strings.TrimPrefix(latest, "v") == strings.TrimPrefix(Version, "v") {
		fmt.Printf("Already up to date (%s).\n", Version)
		return nil
	}

	fmt.Printf("New version available: %s\n", latest)

	// Build download URL.
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	assetName := fmt.Sprintf("nagobot-%s-%s", goos, goarch)
	if goos == "windows" {
		assetName += ".exe"
	}
	url := fmt.Sprintf("https://github.com/linanwx/nagobot/releases/download/%s/%s", latest, assetName)

	installDir := service.DefaultInstallDir()
	binName := service.DefaultBinName()
	installPath := filepath.Join(installDir, binName)

	if err := os.MkdirAll(installDir, 0755); err != nil {
		return fmt.Errorf("cannot create directory %s: %w", installDir, err)
	}

	// Write to temp file in same directory, then rename.
	tmpFile, err := os.CreateTemp(installDir, "nagobot-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // clean up on error

	// Download to temp file. Each mirror gets its own timeout; on failure the next is tried.
	fmt.Printf("Downloading %s...\n", assetName)
	if err := downloadWithFallback(url, tmpFile); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	// macOS: remove quarantine attribute.
	if goos == "darwin" {
		removeQuarantine(tmpPath)
	}

	// Replace: remove old, rename new.
	os.Remove(installPath)
	if err := os.Rename(tmpPath, installPath); err != nil {
		return fmt.Errorf("cannot replace binary: %w", err)
	}

	fmt.Printf("Updated to %s at %s\n", latest, installPath)

	// Sync template files using the NEW binary (current process has old embedded templates).
	fmt.Println("Syncing templates...")
	syncCmd := exec.Command(installPath, "onboard", "--sync")
	syncCmd.Stdout = os.Stdout
	syncCmd.Stderr = os.Stderr
	if err := syncCmd.Run(); err != nil {
		fmt.Printf("Warning: failed to sync templates: %v\n", err)
	}

	// Gracefully stop the running process via socket RPC before restarting.
	// This handles the case where the process was started manually (e.g., nohup)
	// and the service manager cannot stop it.
	stopRunningProcess()

	// Restart service.
	fmt.Println("Restarting service...")
	mgr := service.New()
	if err := mgr.Restart(); err != nil {
		fmt.Printf("    Warning: service restart failed: %v\n", err)
		fmt.Println("    You may need to restart manually.")
	} else {
		fmt.Println("    Service restarted.")
	}

	return nil
}

// stopRunningProcess sends a shutdown RPC to the running nagobot process via
// the unix socket. This ensures the old process exits even if it was started
// manually (nohup) and is not managed by the system service manager.
func stopRunningProcess() {
	socketPath, err := config.SocketPath()
	if err != nil {
		return
	}

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		// No running process or socket not available — nothing to stop.
		return
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Send shutdown RPC.
	req := struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}{ID: "shutdown", Method: "shutdown"}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return
	}

	// Read the response (best effort).
	var resp json.RawMessage
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	fmt.Println("Waiting for old process to exit...")

	// Wait up to 5 seconds for the socket to become unavailable (process exited).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		probe, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
		if err != nil {
			// Socket gone — old process has exited.
			return
		}
		probe.Close()
	}
	fmt.Println("    Warning: old process may still be running.")
}

// China mirrors for GitHub release downloads.
var chinaMirrors = []string{
	"https://gh-proxy.com/",
	"https://ghfast.top/",
	"https://gh-proxy.org/",
}

const perMirrorTimeout = 2 * time.Minute

// rankMirrors probes each mirror in parallel by downloading the first 100KB
// of rawURL and returns mirrors sorted by speed (fastest first).
// Mirrors that fail or are too slow are appended at the end.
func rankMirrors(mirrors []string, rawURL string) []string {
	type result struct {
		mirror string
		speed  float64 // bytes per second, 0 = failed
	}

	results := make([]result, len(mirrors))
	var wg sync.WaitGroup

	for i, mirror := range mirrors {
		wg.Add(1)
		go func(idx int, m string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "GET", m+rawURL, nil)
			req.Header.Set("Range", "bytes=0-102399") // first 100KB
			start := time.Now()
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results[idx] = result{m, 0}
				return
			}
			n, _ := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			elapsed := time.Since(start).Seconds()
			if elapsed > 0 && n > 0 {
				results[idx] = result{m, float64(n) / elapsed}
			} else {
				results[idx] = result{m, 0}
			}
		}(i, mirror)
	}
	wg.Wait()

	// Sort: fastest first, failed last.
	sort.Slice(results, func(i, j int) bool {
		return results[i].speed > results[j].speed
	})

	ranked := make([]string, len(results))
	for i, r := range results {
		ranked[i] = r.mirror
		tag := "failed"
		if r.speed > 0 {
			tag = fmt.Sprintf("%.0f KB/s", r.speed/1024)
		}
		fmt.Printf("    %s %s\n", r.mirror, tag)
	}
	return ranked
}

// downloadWithFallback downloads rawURL into dst, trying mirrors with
// per-mirror timeouts. Each attempt includes the full body transfer;
// on timeout or failure the next mirror is tried automatically.
func downloadWithFallback(rawURL string, dst *os.File) error {
	client := &http.Client{} // no global timeout; per-request context handles it

	type source struct {
		label string
		url   string
	}
	var sources []source

	if isMainlandChina() {
		fmt.Println("    Detected mainland China, ranking mirrors...")
		ranked := rankMirrors(chinaMirrors, rawURL)
		for _, mirror := range ranked {
			sources = append(sources, source{mirror, mirror + rawURL})
		}
		sources = append(sources, source{"direct", rawURL})
	} else {
		sources = append(sources, source{"direct", rawURL})
		sources = append(sources, source{chinaMirrors[0], chinaMirrors[0] + rawURL})
	}

	for _, s := range sources {
		fmt.Printf("    Trying %s\n", s.label)

		ctx, cancel := context.WithTimeout(context.Background(), perMirrorTimeout)
		req, _ := http.NewRequestWithContext(ctx, "GET", s.url, nil)
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			fmt.Printf("    Failed: %v\n", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			cancel()
			fmt.Printf("    Failed: HTTP %s\n", resp.Status)
			continue
		}

		// Reset file for this attempt.
		dst.Seek(0, io.SeekStart)
		dst.Truncate(0)

		var src io.Reader = resp.Body
		if resp.ContentLength > 0 {
			src = &progressReader{r: resp.Body, total: resp.ContentLength}
		}
		_, err = io.Copy(dst, src)
		resp.Body.Close()
		cancel()

		if err != nil {
			fmt.Println() // newline after progress bar
			fmt.Printf("    Failed: %v\n", err)
			continue
		}
		if resp.ContentLength > 0 {
			fmt.Println() // newline after progress bar
		}
		return nil
	}
	return fmt.Errorf("all download attempts failed")
}

// isMainlandChina checks if the current machine is in mainland China
// by querying ipinfo.io/country.
func isMainlandChina() bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://ipinfo.io/country")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(body)) == "CN"
}

// progressReader wraps an io.Reader and prints a progress bar to stdout.
type progressReader struct {
	r       io.Reader
	total   int64
	current int64
	last    int // last printed percentage (avoid redundant writes)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.current += int64(n)

	pct := int(pr.current * 100 / pr.total)
	if pct != pr.last || err == io.EOF {
		pr.last = pct
		filled := pct / 2          // 50-char wide bar
		empty := 50 - filled
		fmt.Fprintf(os.Stdout, "\r    %s / %s  [%s%s]  %d%%",
			formatBytes(pr.current), formatBytes(pr.total),
			strings.Repeat("=", filled), strings.Repeat(" ", empty),
			pct)
	}
	return n, err
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
