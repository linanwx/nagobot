package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var userMsgStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan

type chatEntry struct {
	text   string
	isUser bool
}

// ChatPanel displays conversation history in a scrollable viewport.
type ChatPanel struct {
	viewport viewport.Model
	entries  []chatEntry // raw messages for re-wrapping on resize
	width    int
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
		p.entries = append(p.entries, chatEntry{text: msg.Text, isUser: msg.IsUser})
		p.rebuildContent()
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
	p.width = width
	p.viewport.Width = width
	p.viewport.Height = height
	p.rebuildContent()
}

func (p *ChatPanel) rebuildContent() {
	if p.width <= 0 {
		return
	}
	wrap := lipgloss.NewStyle().Width(p.width)
	lines := make([]string, 0, len(p.entries))
	for _, e := range p.entries {
		if e.isUser {
			lines = append(lines, wrap.Render(userMsgStyle.Render("> "+e.text)))
		} else {
			lines = append(lines, wrap.Render(e.text))
		}
	}
	p.viewport.SetContent(strings.Join(lines, "\n"))
}
