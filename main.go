// nagobot is a lightweight, Go-based AI assistant.
package main

import (
	"fmt"
	"os"

	"github.com/linanwx/nagobot/cmd"
	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}
	workspace, _ := cfg.WorkspacePath()
	if err := logger.Init(cfg.BuildLoggerConfig(), workspace); err != nil {
		fmt.Fprintln(os.Stderr, "logger init error:", err)
	}
	cmd.Execute()
}
