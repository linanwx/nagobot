package config

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/linanwx/nagobot/logger"
	"gopkg.in/yaml.v3"
)

// fileMu protects config file access.
// Save takes a write lock; Load takes a read lock around ReadFile.
var fileMu sync.RWMutex

// Load loads the configuration from disk.
// It only writes back to disk when applyDefaults() actually modified a field.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	// Read lock prevents reading a mid-write file within the same process.
	fileMu.RLock()
	data, err := os.ReadFile(path)
	fileMu.RUnlock()

	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			cfg.applyDefaults()
			if err := cfg.Save(); err != nil {
				logger.Warn("failed to save default config", "err", err)
			}
			return cfg, nil
		}
		return nil, err
	}

	// Defensive: treat empty file as missing to avoid zero-value config overwriting real data.
	if len(data) == 0 {
		logger.Warn("config file is empty, using defaults")
		cfg := DefaultConfig()
		cfg.applyDefaults()
		if err := cfg.Save(); err != nil {
			logger.Warn("failed to save default config", "err", err)
		}
		return cfg, nil
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	migrated := cfg.migrateLegacyModelNames()
	defaultsChanged := cfg.applyDefaults()
	if migrated || defaultsChanged {
		if err := cfg.Save(); err != nil {
			logger.Warn("failed to persist config changes", "err", err, "migrated", migrated, "defaultsChanged", defaultsChanged)
		}
	}
	return &cfg, nil
}

// Save saves the configuration to config.yaml atomically.
// It writes to a temporary file first, then renames to prevent corruption.
func (c *Config) Save() error {
	fileMu.Lock()
	defer fileMu.Unlock()

	path, err := ConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename.
	// os.Rename on the same filesystem is atomic on macOS/Linux,
	// so concurrent readers never see a truncated file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
