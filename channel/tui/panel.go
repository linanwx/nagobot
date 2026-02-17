// Package tui provides a terminal user interface for the CLI channel.
package tui

import tea "github.com/charmbracelet/bubbletea"

// Panel is a composable TUI region with its own state, update logic, and view.
// The root App model orchestrates panels without knowing their internals.
type Panel interface {
	Update(tea.Msg) (Panel, tea.Cmd)
	View() string
	SetSize(width, height int)
}

// LogLineMsg carries a single log line from the logger writer.
type LogLineMsg struct{ Line string }

// ChatMsg carries a chat message to display in the conversation panel.
type ChatMsg struct {
	Text   string
	IsUser bool
}

// InputSubmitMsg is emitted when the user presses Enter in the input panel.
type InputSubmitMsg struct{ Text string }
