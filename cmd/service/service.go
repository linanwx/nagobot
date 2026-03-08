package service

import (
	"os"
	"path/filepath"
	"runtime"
)

// Manager handles platform-specific service registration.
type Manager interface {
	// Install registers nagobot as a system service.
	Install(binPath, logDir string) error
	// InstallElevated registers nagobot as a system service with elevation if needed.
	InstallElevated(binPath, logDir string) error
	// Uninstall stops and removes the system service.
	Uninstall() error
	// Restart restarts the running service.
	Restart() error
}

// DefaultInstallDir returns the platform-appropriate binary install directory.
func DefaultInstallDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(local, "nagobot")
	default:
		return filepath.Join(home, ".local", "bin")
	}
}

// DefaultBinName returns the binary name for the current OS.
func DefaultBinName() string {
	if runtime.GOOS == "windows" {
		return "nagobot.exe"
	}
	return "nagobot"
}
