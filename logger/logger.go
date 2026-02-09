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
)

// Init initializes the logger with the provided config.
func Init(cfg Config, configDir string) error {
	mu.Lock()
	defer mu.Unlock()

	if !cfg.Enabled {
		enabled = false
		base = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
		return nil
	}

	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var writers []io.Writer
	var initErr error
	if cfg.Stdout {
		writers = append(writers, os.Stdout)
	}
	if cfg.File != "" {
		path := expandPath(cfg.File, configDir)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("logger: create log dir: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			initErr = fmt.Errorf("logger: open log file: %w", err)
		} else {
			writers = append(writers, f)
		}
	}
	if len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}
	handler := slog.NewTextHandler(io.MultiWriter(writers...), opts)
	base = slog.New(handler)
	enabled = true
	return initErr
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

