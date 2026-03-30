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
	"github.com/linanwx/nagobot/logger"
	"github.com/spf13/cobra"
)

var updatePre bool

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update nagobot to the latest version",
	Long: `Check for the latest release on GitHub and update the binary in place.

By default only stable (non-pre-release) versions are considered.
Use --pre to include pre-release versions.

When a serve process is running, the update is delegated to it via RPC.
Otherwise the CLI performs the update directly.`,
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

// ---------------------------------------------------------------------------
// runUpdate: RPC-first, with fallback to direct execution.
// ---------------------------------------------------------------------------

func runUpdate(cmd *cobra.Command, args []string) error {
	fmt.Printf("Current version: %s\n", Version)

	// Try RPC mode: delegate to running serve process.
	result, err := rpcCallWithTimeout("update.start", updateStartParams{Pre: updatePre}, 5*time.Second)
	if err != nil {
		// Serve not running or RPC failed — fall back to direct update.
		return runUpdateDirect()
	}

	var startResp updateStartResponse
	if err := json.Unmarshal(result, &startResp); err != nil {
		return runUpdateDirect()
	}
	if !startResp.Accepted {
		return fmt.Errorf("update rejected: %s", startResp.Reason)
	}

	fmt.Println("Update delegated to running service...")

	// Poll update.status until done or connection lost.
	var lastMsg string
	for {
		time.Sleep(500 * time.Millisecond)

		result, err := rpcCallWithTimeout("update.status", nil, 3*time.Second)
		if err != nil {
			// Connection lost = serve is restarting with new version.
			fmt.Println("\nService is restarting with the new version.")
			return nil
		}

		var status updateStatusResponse
		if err := json.Unmarshal(result, &status); err != nil {
			continue
		}

		printUpdateProgress(status, &lastMsg)

		if status.Done {
			if status.Error != "" {
				return fmt.Errorf("\nUpdate failed: %s", status.Error)
			}
			if status.Phase == phaseUpToDate {
				fmt.Printf("\nAlready up to date (%s).\n", Version)
			} else {
				fmt.Printf("\nUpdated to %s. Service is restarting.\n", status.Latest)
			}
			return nil
		}
	}
}

func printUpdateProgress(s updateStatusResponse, lastMsg *string) {
	var msg string
	switch s.Phase {
	case phaseChecking:
		msg = "Checking for updates..."
	case phaseRankingMirrors:
		msg = "Ranking mirrors..."
	case phaseDownloading:
		if s.Progress > 0 {
			msg = fmt.Sprintf("%s  %d%%", s.Message, s.Progress)
		} else {
			msg = s.Message
		}
	case phaseInstalling:
		msg = fmt.Sprintf("Installing %s...", s.Latest)
	case phaseSyncing:
		msg = "Syncing templates..."
	case phaseRestarting:
		msg = s.Message
	default:
		msg = s.Message
	}
	if msg != *lastMsg {
		fmt.Printf("\r\033[K%s", msg)
		*lastMsg = msg
	}
}

// ---------------------------------------------------------------------------
// runUpdateDirect: full update when serve is not running (fallback).
// ---------------------------------------------------------------------------

func runUpdateDirect() error {
	fmt.Println("No running service detected, updating directly...")

	latest, err := fetchLatestVersion(updatePre)
	if err != nil {
		return err
	}

	if strings.TrimPrefix(latest, "v") == strings.TrimPrefix(Version, "v") {
		fmt.Printf("Already up to date (%s).\n", Version)
		return nil
	}

	fmt.Printf("New version available: %s\n", latest)

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

	tmpFile, err := os.CreateTemp(installDir, "nagobot-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	fmt.Printf("Downloading %s...\n", assetName)
	if err := downloadWithFallback(url, tmpFile, printDownloadProgress); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	if goos == "darwin" {
		removeQuarantine(tmpPath)
	}

	os.Remove(installPath)
	if err := os.Rename(tmpPath, installPath); err != nil {
		return fmt.Errorf("cannot replace binary: %w", err)
	}

	fmt.Printf("Updated to %s at %s\n", latest, installPath)

	// Sync templates using the new binary.
	fmt.Println("Syncing templates...")
	syncCmd := exec.Command(installPath, "onboard", "--sync")
	syncCmd.Stdout = os.Stdout
	syncCmd.Stderr = os.Stderr
	if err := syncCmd.Run(); err != nil {
		fmt.Printf("Warning: failed to sync templates: %v\n", err)
	}

	// Gracefully stop the running process (if any) before restarting.
	stopRunningProcess()

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

// printDownloadProgress is the progress callback for direct (non-RPC) mode.
func printDownloadProgress(mirror string, pct int) {
	fmt.Fprintf(os.Stdout, "\r    %s  %d%%", mirror, pct)
}

// ---------------------------------------------------------------------------
// stopRunningProcess sends a shutdown RPC (used by direct update only).
// ---------------------------------------------------------------------------

func stopRunningProcess() {
	socketPath, err := config.SocketPath()
	if err != nil {
		return
	}

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	req := struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}{ID: "shutdown", Method: "shutdown"}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return
	}

	var resp json.RawMessage
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	fmt.Println("Waiting for old process to exit...")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		probe, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
		if err != nil {
			return
		}
		probe.Close()
	}
	fmt.Println("    Warning: old process may still be running.")
}

// ---------------------------------------------------------------------------
// Download helpers (shared by direct mode and serve-side updater).
// ---------------------------------------------------------------------------

// China mirrors for GitHub release downloads.
var chinaMirrors = []string{
	"https://gh-proxy.com/",
	"https://ghfast.top/",
	"https://gh-proxy.org/",
}

const perMirrorTimeout = 2 * time.Minute

// rankMirrors probes each mirror in parallel and returns them sorted by speed.
func rankMirrors(mirrors []string, rawURL string) []string {
	type result struct {
		mirror string
		speed  float64
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
			req.Header.Set("Range", "bytes=0-102399")
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
		logger.Info("mirror ranked", "mirror", r.mirror, "speed", tag)
	}
	return ranked
}

// downloadWithFallback downloads rawURL into dst, trying mirrors with
// per-mirror timeouts. onProgress is called with (mirror label, percent).
func downloadWithFallback(rawURL string, dst *os.File, onProgress func(mirror string, pct int)) error {
	client := &http.Client{}

	type source struct {
		label string
		url   string
	}
	var sources []source

	if isMainlandChina() {
		logger.Info("detected mainland China, ranking mirrors")
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
		logger.Info("trying download source", "source", s.label)

		ctx, cancel := context.WithTimeout(context.Background(), perMirrorTimeout)
		req, _ := http.NewRequestWithContext(ctx, "GET", s.url, nil)
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			logger.Warn("download source failed", "source", s.label, "error", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			cancel()
			logger.Warn("download source failed", "source", s.label, "status", resp.Status)
			continue
		}

		dst.Seek(0, io.SeekStart)
		dst.Truncate(0)

		var src io.Reader = resp.Body
		if resp.ContentLength > 0 {
			src = &progressReader{
				r:          resp.Body,
				total:      resp.ContentLength,
				label:      s.label,
				onProgress: onProgress,
			}
		}
		_, err = io.Copy(dst, src)
		resp.Body.Close()
		cancel()

		if err != nil {
			logger.Warn("download body failed", "source", s.label, "error", err)
			continue
		}
		return nil
	}
	return fmt.Errorf("all download attempts failed")
}

// isMainlandChina checks if the current machine is in mainland China.
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

// progressReader wraps an io.Reader and reports download progress.
type progressReader struct {
	r          io.Reader
	total      int64
	current    int64
	last       int
	label      string
	onProgress func(mirror string, pct int)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.current += int64(n)

	pct := int(pr.current * 100 / pr.total)
	if pct != pr.last || err == io.EOF {
		pr.last = pct
		if pr.onProgress != nil {
			pr.onProgress(pr.label, pct)
		}
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
