package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/linanwx/nagobot/cmd/service"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update nagobot to the latest version",
	Long: `Check for the latest release on GitHub and update the binary in place.

After replacing the binary the running service is automatically restarted.`,
	RunE: runUpdate,
}

func init() {
	rootCmd.AddCommand(updateCmd)
}

type ghRelease struct {
	TagName string `json:"tag_name"`
}

func runUpdate(cmd *cobra.Command, args []string) error {
	fmt.Printf("Current version: %s\n", Version)

	// Fetch latest release.
	resp, err := http.Get("https://api.github.com/repos/linanwx/nagobot/releases/latest")
	if err != nil {
		return fmt.Errorf("cannot reach GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("cannot parse release info: %w", err)
	}

	latest := release.TagName
	if latest == "" {
		return fmt.Errorf("no release found")
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

	// Download to temp file.
	fmt.Printf("Downloading %s...\n", assetName)
	dlResp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", dlResp.Status)
	}

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

	if _, err := io.Copy(tmpFile, dlResp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download write failed: %w", err)
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

	// Sync template files (skills, agents, scripts) to workspace.
	if err := runSync(); err != nil {
		fmt.Printf("Warning: failed to sync templates: %v\n", err)
	}

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
