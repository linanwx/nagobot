package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// saveMu serializes concurrent Config.Save() calls to prevent file corruption.
var saveMu sync.Mutex

// Load loads the configuration from disk.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("config not found, run 'nagobot onboard' first")
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	cfg.applyDefaults()
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save saves the configuration to config.yaml.
// Concurrent calls are serialized to prevent file corruption.
func (c *Config) Save() error {
	saveMu.Lock()
	defer saveMu.Unlock()

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

	return os.WriteFile(path, data, 0600)
}
