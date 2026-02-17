// Package logger provides a minimal slog-based logging wrapper.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config describes logger settings.
type Config struct {
	Enabled bool
	Level   string
	Stdout  bool
	File    string
}

var (
	mu      sync.RWMutex
	base    *slog.Logger
	enabled = true

	// Saved state for Intercept/Restore.
	savedCfg  Config
	savedFile *os.File    // log file opened during Init
	intercept io.Writer   // non-nil when TUI has intercepted stdout
)

// Init initializes the logger with the provided config.
func Init(cfg Config, configDir string) error {
	mu.Lock()
	defer mu.Unlock()

	savedCfg = cfg

	if !cfg.Enabled {
		enabled = false
		base = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
		return nil
	}

	var initErr error
	if cfg.File != "" {
		path := expandPath(cfg.File, configDir)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("logger: create log dir: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			initErr = fmt.Errorf("logger: open log file: %w", err)
		} else {
			savedFile = f
		}
	}

	rebuild()
	return initErr
}

// Intercept replaces stdout with a custom writer (e.g. TUI log panel).
// The file writer (if any) is preserved.
func Intercept(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	intercept = w
	rebuild()
}

// Restore undoes Intercept and restores stdout logging.
func Restore() {
	mu.Lock()
	defer mu.Unlock()
	intercept = nil
	rebuild()
}

// rebuild reconstructs the slog handler from current state.
// Must be called with mu held.
func rebuild() {
	level := parseLevel(savedCfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var writers []io.Writer
	if intercept != nil {
		writers = append(writers, intercept)
	} else if savedCfg.Stdout {
		writers = append(writers, os.Stdout)
	}
	if savedFile != nil {
		writers = append(writers, savedFile)
	}
	if len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}

	base = slog.New(slog.NewTextHandler(io.MultiWriter(writers...), opts))
	enabled = true
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	log(slog.LevelDebug, msg, args...)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	log(slog.LevelInfo, msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	log(slog.LevelWarn, msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	log(slog.LevelError, msg, args...)
}

func log(level slog.Level, msg string, args ...any) {
	mu.RLock()
	l := base
	on := enabled
	mu.RUnlock()

	if !on || l == nil {
		return
	}

	l.Log(nil, level, msg, args...)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func expandPath(path, configDir string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	if filepath.IsAbs(path) {
		return path
	}
	if configDir != "" {
		return filepath.Join(configDir, path)
	}
	return path
}
