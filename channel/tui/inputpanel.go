package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// InputPanel provides a single-line text input with CJK-aware cursor handling.
type InputPanel struct {
	input         textinput.Model
	width, height int
}

// NewInputPanel creates an input panel with the given prompt.
func NewInputPanel(prompt string) *InputPanel {
	ti := textinput.New()
	ti.Prompt = prompt
	ti.Focus()
	return &InputPanel{input: ti}
}

func (p *InputPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyEnter {
			text := p.input.Value()
			if text == "" {
				return p, nil
			}
			p.input.Reset()
			return p, func() tea.Msg { return InputSubmitMsg{Text: text} }
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

func (p *InputPanel) View() string {
	return p.input.View()
}

func (p *InputPanel) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.input.Width = width - len(p.input.Prompt) - 1
}
