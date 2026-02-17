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
	rawLines []string // raw log lines for re-wrapping on resize
	maxLines int
	width    int
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
		p.rawLines = append(p.rawLines, line)
		if len(p.rawLines) > p.maxLines {
			p.rawLines = p.rawLines[len(p.rawLines)-p.maxLines:]
		}
		p.rebuildContent()
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
	p.width = width
	p.viewport.Width = width
	p.viewport.Height = height
	p.rebuildContent()
}

func (p *LogPanel) rebuildContent() {
	if p.width <= 0 {
		return
	}
	wrap := lipgloss.NewStyle().Width(p.width)
	lines := make([]string, 0, len(p.rawLines))
	for _, raw := range p.rawLines {
		lines = append(lines, wrap.Render(logLineStyle.Render(raw)))
	}
	p.viewport.SetContent(strings.Join(lines, "\n"))
}
