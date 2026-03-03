//go:build !darwin && !linux && !windows

package service

import (
	"fmt"
	"runtime"
)

// New returns an unsupported service manager for unrecognized operating systems.
func New() Manager { return &unsupportedManager{} }

type unsupportedManager struct{}

func (m *unsupportedManager) Install(string, string) error {
	return fmt.Errorf("service management is not supported on %s", runtime.GOOS)
}

func (m *unsupportedManager) Uninstall() error {
	return fmt.Errorf("service management is not supported on %s", runtime.GOOS)
}

func (m *unsupportedManager) Restart() error {
	return fmt.Errorf("service management is not supported on %s", runtime.GOOS)
}
