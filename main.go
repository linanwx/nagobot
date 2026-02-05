// nagobot is a lightweight, Go-based AI assistant.
package main

import (
	"fmt"
	"os"

	"github.com/pinkplumcom/nagobot/cmd"
	"github.com/pinkplumcom/nagobot/config"
	"github.com/pinkplumcom/nagobot/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}
	configDir, _ := config.ConfigDir()
	logEnabled := true
	if cfg.Logging.Enabled != nil {
		logEnabled = *cfg.Logging.Enabled
	}
	logCfg := logger.Config{
		Enabled: logEnabled,
		Level:   cfg.Logging.Level,
		Stdout:  cfg.Logging.Stdout,
		File:    cfg.Logging.File,
	}
	if err := logger.Init(logCfg, configDir); err != nil {
		fmt.Fprintln(os.Stderr, "logger init error:", err)
	}
	cmd.Execute()
}
