package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const defaultLogRatio = 0.4

var separatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

// App is the root bubbletea model that orchestrates panels and layout.
type App struct {
	logPanel   Panel
	chatPanel  Panel
	inputPanel Panel

	width, height int
	logRatio      float64

	// InputCh receives user input text from the input panel.
	InputCh chan string
}

// NewApp creates the root TUI model with default panels.
func NewApp() *App {
	return &App{
		logPanel:   NewLogPanel(),
		chatPanel:  NewChatPanel(),
		inputPanel: NewInputPanel("nagobot> "),
		logRatio:   defaultLogRatio,
		InputCh:    make(chan string, 16),
	}
}

func (m *App) Init() tea.Cmd {
	return textinputBlink()
}

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		}
		// All other keys go to input panel.
		p, cmd := m.inputPanel.Update(msg)
		m.inputPanel = p
		cmds = append(cmds, cmd)

	case InputSubmitMsg:
		// Echo user message to chat panel.
		p, cmd := m.chatPanel.Update(ChatMsg{Text: msg.Text, IsUser: true})
		m.chatPanel = p
		cmds = append(cmds, cmd)
		// Send to channel consumer (non-blocking).
		select {
		case m.InputCh <- msg.Text:
		default:
		}

	case LogLineMsg:
		p, cmd := m.logPanel.Update(msg)
		m.logPanel = p
		cmds = append(cmds, cmd)

	case ChatMsg:
		p, cmd := m.chatPanel.Update(msg)
		m.chatPanel = p
		cmds = append(cmds, cmd)

	default:
		// Broadcast unknown messages to input panel (e.g. blink cursor).
		p, cmd := m.inputPanel.Update(msg)
		m.inputPanel = p
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *App) View() string {
	if m.width == 0 || m.height == 0 {
		return "initializing..."
	}

	sep := separatorStyle.Render(strings.Repeat("─", m.width))

	return lipgloss.JoinVertical(lipgloss.Left,
		m.logPanel.View(),
		sep,
		m.chatPanel.View(),
		sep,
		m.inputPanel.View(),
	)
}

func (m *App) recalcLayout() {
	const inputH = 1
	const sepLines = 2 // two separator lines

	usable := max(m.height-inputH-sepLines, 2)
	logH := max(int(float64(usable)*m.logRatio), 1)
	chatH := max(usable-logH, 1)

	m.logPanel.SetSize(m.width, logH)
	m.chatPanel.SetSize(m.width, chatH)
	m.inputPanel.SetSize(m.width, inputH)
}

// textinputBlink returns the initial blink command for the textinput cursor.
func textinputBlink() tea.Cmd {
	return nil // textinput handles its own blink when focused
}
