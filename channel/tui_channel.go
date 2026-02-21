package channel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/linanwx/nagobot/channel/tui"
	"github.com/linanwx/nagobot/logger"
)

const tuiMessageBufferSize = 64

// TUIChannel implements the Channel interface using a bubbletea TUI.
type TUIChannel struct {
	app      *tui.App
	program  *tea.Program
	messages chan *Message
	done     chan struct{}
	wg       sync.WaitGroup
	msgID    atomic.Int64
	stopOnce sync.Once
}

func newTUIChannel() *TUIChannel {
	return &TUIChannel{
		messages: make(chan *Message, tuiMessageBufferSize),
		done:     make(chan struct{}),
	}
}

func (c *TUIChannel) Name() string { return "cli" }

func (c *TUIChannel) Start(ctx context.Context) error {
	c.app = tui.NewApp()
	c.program = tea.NewProgram(c.app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Redirect logger output to the TUI log panel.
	lw := &logWriter{program: c.program}
	logger.Intercept(lw)

	// Run bubbletea in a goroutine.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if _, err := c.program.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		}
		// TUI exited — signal stop and terminate the process.
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		// Send SIGINT to trigger serve.go's graceful shutdown.
		p, _ := os.FindProcess(os.Getpid())
		if p != nil {
			_ = p.Signal(syscall.SIGINT)
		}
	}()

	// Read user input from the App and produce Messages.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.done:
				return
			case text, ok := <-c.app.InputCh:
				if !ok {
					return
				}
				if text == "exit" || text == "quit" || text == "/exit" || text == "/quit" {
					c.program.Quit()
					return
				}
				id := c.msgID.Add(1)
				c.messages <- &Message{
					ID:        fmt.Sprintf("cli-%d", id),
					ChannelID: "cli:local",
					UserID:    "local",
					Username:  os.Getenv("USER"),
					Text:      text,
					Metadata:  make(map[string]string),
				}
			}
		}
	}()

	logger.Info("cli channel started (TUI mode)")
	return nil
}

func (c *TUIChannel) Stop() error {
	c.stopOnce.Do(func() {
		close(c.done)
		if c.program != nil {
			c.program.Quit()
		}
		c.wg.Wait()
		logger.Restore()
		close(c.messages)
		logger.Info("cli channel stopped")
	})
	return nil
}

func (c *TUIChannel) Send(_ context.Context, resp *Response) error {
	if c.program == nil {
		return nil
	}
	c.program.Send(tui.ChatMsg{Text: resp.Text, IsUser: false})
	return nil
}

func (c *TUIChannel) Messages() <-chan *Message {
	return c.messages
}

// logWriter implements io.Writer and sends each write as a LogLineMsg to the TUI.
type logWriter struct {
	program *tea.Program
}

func (w *logWriter) Write(p []byte) (int, error) {
	// Split on newlines in case a single write contains multiple lines.
	lines := bytes.Split(p, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		w.program.Send(tui.LogLineMsg{Line: string(line)})
	}
	return len(p), nil
}
