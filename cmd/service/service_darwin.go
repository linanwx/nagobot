package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

// New returns a macOS service manager.
func New() Manager { return &darwinManager{} }

const (
	darwinLabel = "com.nagobot.serve"
)

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.BinPath}}</string>
        <string>serve</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>StandardOutPath</key>
    <string>{{.LogDir}}/launchd-stdout.log</string>

    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/launchd-stderr.log</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>{{.Home}}</string>
        <key>PATH</key>
        <string>{{.Path}}</string>
    </dict>
</dict>
</plist>
`))

type plistData struct {
	Label   string
	BinPath string
	LogDir  string
	Home    string
	Path    string
}

type darwinManager struct{}

func (m *darwinManager) Install(binPath, logDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", darwinLabel+".plist")

	// Stop existing service if any.
	_ = exec.Command("launchctl", "unload", plistPath).Run()

	// Detect Homebrew prefix for PATH.
	brewPrefix := detectBrewPrefix()
	localBin := filepath.Join(home, ".local", "bin")
	pathEnv := fmt.Sprintf("%s:%s/bin:%s/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", localBin, brewPrefix, brewPrefix)

	// Generate plist.
	f, err := os.Create(plistPath)
	if err != nil {
		return fmt.Errorf("cannot create plist at %s: %w", plistPath, err)
	}
	defer f.Close()

	if err := plistTemplate.Execute(f, plistData{
		Label:   darwinLabel,
		BinPath: binPath,
		LogDir:  logDir,
		Home:    home,
		Path:    pathEnv,
	}); err != nil {
		return fmt.Errorf("cannot write plist: %w", err)
	}

	// Load service.
	if out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load failed: %s (%w)", string(out), err)
	}

	fmt.Println("    Service: launchctl print gui/$(id -u)/" + darwinLabel)
	return nil
}

func (m *darwinManager) Uninstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", darwinLabel+".plist")

	fmt.Println("==> Stopping service...")
	_ = exec.Command("launchctl", "unload", plistPath).Run()

	fmt.Println("==> Removing launchd plist...")
	os.Remove(plistPath)

	return nil
}

func detectBrewPrefix() string {
	if info, err := os.Stat("/opt/homebrew"); err == nil && info.IsDir() {
		return "/opt/homebrew"
	}
	return "/usr/local"
}
