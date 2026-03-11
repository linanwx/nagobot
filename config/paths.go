package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ConfigDir returns the nagobot config directory (~/.nagobot).
func ConfigDir() (string, error) {
	if configDirOverride != "" {
		dir := configDirOverride
		if dir == "~" || strings.HasPrefix(dir, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			if dir == "~" {
				return home, nil
			}
			return filepath.Join(home, dir[2:]), nil
		}
		if filepath.IsAbs(dir) {
			return filepath.Clean(dir), nil
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		return filepath.Clean(abs), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nagobot"), nil
}

// SocketPath returns the path to the daemon unix socket.
func SocketPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nagobot.sock"), nil
}

// ConfigPath returns the default YAML config path.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// WorkspacePath returns the workspace path, expanding ~ if needed.
func (c *Config) WorkspacePath() (string, error) {
	ws := c.Thread.Workspace
	if ws == "" {
		dir, err := ConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "workspace"), nil
	}

	// Expand ~ to home directory
	if len(ws) > 0 && ws[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		ws = filepath.Join(home, ws[1:])
	}
	return ws, nil
}

// EnsureWorkspace creates the workspace directory if it doesn't exist.
func (c *Config) EnsureWorkspace() error {
	ws, err := c.WorkspacePath()
	if err != nil {
		return err
	}
	return os.MkdirAll(ws, 0755)
}
