package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/cmd/service"
	"github.com/linanwx/nagobot/logger"
)

// Update phases reported to CLI via update.status RPC.
const (
	phaseChecking       = "checking"
	phaseRankingMirrors  = "ranking_mirrors"
	phaseDownloading     = "downloading"
	phaseInstalling      = "installing"
	phaseSyncing         = "syncing"
	phaseRestarting      = "restarting"
	phaseUpToDate        = "up_to_date"
	phaseFailed          = "failed"
)

// updateStartParams is the RPC request for update.start.
type updateStartParams struct {
	Pre bool `json:"pre"`
}

// updateStartResponse is the RPC response for update.start.
type updateStartResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
	Current  string `json:"current"`
}

// updateStatusResponse is the RPC response for update.status.
type updateStatusResponse struct {
	Phase    string `json:"phase"`
	Message  string `json:"message"`
	Progress int    `json:"progress"` // 0-100, downloading phase only
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
	Current  string `json:"current,omitempty"`
	Latest   string `json:"latest,omitempty"`
}

// updater runs the update workflow inside the serve process.
type updater struct {
	mu       sync.Mutex
	running  bool
	phase    string
	message  string
	progress int
	done     bool
	err      string
	latest   string
}

// Start begins a background update. Returns false if one is already running.
func (u *updater) Start(pre bool) (bool, string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.running {
		return false, "update already in progress"
	}
	u.running = true
	u.phase = phaseChecking
	u.message = "Checking for updates..."
	u.progress = 0
	u.done = false
	u.err = ""
	u.latest = ""
	go u.run(pre)
	return true, ""
}

// Status returns a snapshot of the current update state.
func (u *updater) Status() updateStatusResponse {
	u.mu.Lock()
	defer u.mu.Unlock()
	return updateStatusResponse{
		Phase:    u.phase,
		Message:  u.message,
		Progress: u.progress,
		Done:     u.done,
		Error:    u.err,
		Current:  Version,
		Latest:   u.latest,
	}
}

func (u *updater) setPhase(phase, message string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.phase = phase
	u.message = message
	u.progress = 0
}

func (u *updater) setProgress(mirror string, pct int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.progress = pct
	if mirror != "" {
		u.message = fmt.Sprintf("Downloading via %s", mirror)
	}
}

func (u *updater) fail(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.phase = phaseFailed
	u.err = fmt.Sprintf(format, args...)
	u.message = u.err
	u.done = true
	u.running = false
	logger.Error("update failed", "error", u.err)
}

func (u *updater) finish(phase, message string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.phase = phase
	u.message = message
	u.done = true
	u.running = false
}

// run executes the full update pipeline in the serve process.
func (u *updater) run(pre bool) {
	// 1. Check latest version.
	u.setPhase(phaseChecking, "Checking for updates...")
	latest, err := fetchLatestVersion(pre)
	if err != nil {
		u.fail("check version: %v", err)
		return
	}

	u.mu.Lock()
	u.latest = latest
	u.mu.Unlock()

	if strings.TrimPrefix(latest, "v") == strings.TrimPrefix(Version, "v") {
		u.finish(phaseUpToDate, fmt.Sprintf("Already up to date (%s)", Version))
		return
	}

	logger.Info("update available", "current", Version, "latest", latest)

	// 2. Build download URL.
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
		u.fail("create directory %s: %v", installDir, err)
		return
	}

	// 3. Create temp file.
	tmpFile, err := os.CreateTemp(installDir, "nagobot-update-*")
	if err != nil {
		u.fail("create temp file: %v", err)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// 4. Download (includes mirror ranking for mainland China).
	u.setPhase(phaseDownloading, "Downloading "+assetName)
	if err := downloadWithFallback(url, tmpFile, u.setProgress); err != nil {
		tmpFile.Close()
		u.fail("download: %v", err)
		return
	}
	tmpFile.Close()

	// 6. Install.
	u.setPhase(phaseInstalling, "Installing "+latest)
	if err := os.Chmod(tmpPath, 0755); err != nil {
		u.fail("chmod: %v", err)
		return
	}
	if goos == "darwin" {
		removeQuarantine(tmpPath)
	}
	os.Remove(installPath)
	if err := os.Rename(tmpPath, installPath); err != nil {
		u.fail("replace binary: %v", err)
		return
	}

	// 7. Sync templates with new binary.
	u.setPhase(phaseSyncing, "Syncing templates...")
	syncCmd := exec.Command(installPath, "onboard", "--sync")
	if out, err := syncCmd.CombinedOutput(); err != nil {
		logger.Warn("template sync failed", "error", err, "output", string(out))
		// Non-fatal: continue to restart.
	}

	// 8. Restart.
	logger.Info("update installed, restarting", "version", latest)
	u.finish(phaseRestarting, fmt.Sprintf("Updated to %s, restarting...", latest))

	// Give CLI a window to poll the final status before we die.
	time.Sleep(500 * time.Millisecond)

	mgr := service.New()
	if err := mgr.Restart(); err != nil {
		// Restart failed — we're still alive, report the error.
		u.fail("restart: %v", err)
		return
	}
	// On success Restart() kills this process; we never reach here.
}
