package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const defaultMaxLogLines = 1000

var logLineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // dim gray

// LogPanel displays log output in a scrollable viewport.
type LogPanel struct {
	viewport viewport.Model
	lines    []string
	maxLines int
}

// NewLogPanel creates a log panel.
func NewLogPanel() *LogPanel {
	vp := viewport.New(0, 0)
	vp.SetContent("")
	return &LogPanel{
		viewport: vp,
		maxLines: defaultMaxLogLines,
	}
}

func (p *LogPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case LogLineMsg:
		line := strings.TrimRight(msg.Line, "\n")
		p.lines = append(p.lines, logLineStyle.Render(line))
		if len(p.lines) > p.maxLines {
			p.lines = p.lines[len(p.lines)-p.maxLines:]
		}
		p.viewport.SetContent(strings.Join(p.lines, "\n"))
		p.viewport.GotoBottom()
		return p, nil
	}
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return p, cmd
}

func (p *LogPanel) View() string {
	return p.viewport.View()
}

func (p *LogPanel) SetSize(width, height int) {
	p.viewport.Width = width
	p.viewport.Height = height
}
