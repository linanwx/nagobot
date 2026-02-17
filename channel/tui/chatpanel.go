package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var userMsgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan

// ChatPanel displays conversation history in a scrollable viewport.
type ChatPanel struct {
	viewport viewport.Model
	lines    []string
}

// NewChatPanel creates a chat panel.
func NewChatPanel() *ChatPanel {
	vp := viewport.New(0, 0)
	vp.SetContent("")
	return &ChatPanel{viewport: vp}
}

func (p *ChatPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case ChatMsg:
		var line string
		if msg.IsUser {
			line = userMsgStyle.Render("> " + msg.Text)
		} else {
			line = msg.Text
		}
		p.lines = append(p.lines, line)
		p.viewport.SetContent(strings.Join(p.lines, "\n"))
		p.viewport.GotoBottom()
		return p, nil
	}
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return p, cmd
}

func (p *ChatPanel) View() string {
	return p.viewport.View()
}

func (p *ChatPanel) SetSize(width, height int) {
	p.viewport.Width = width
	p.viewport.Height = height
}
