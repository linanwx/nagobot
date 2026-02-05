package channel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/linanwx/nagobot/logger"
)

// CLIChannel implements the Channel interface for interactive CLI.
type CLIChannel struct {
	prompt   string
	messages chan *Message
	done     chan struct{}
	wg       sync.WaitGroup
	msgID    int64
}

// CLIConfig holds CLI channel configuration.
type CLIConfig struct {
	Prompt string // Input prompt (default: "> ")
}

// NewCLIChannel creates a new CLI channel.
func NewCLIChannel(cfg CLIConfig) *CLIChannel {
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = "> "
	}

	return &CLIChannel{
		prompt:   prompt,
		messages: make(chan *Message, 10),
		done:     make(chan struct{}),
	}
}

// Name returns the channel name.
func (c *CLIChannel) Name() string {
	return "cli"
}

// Start begins reading from stdin.
func (c *CLIChannel) Start(ctx context.Context) error {
	logger.Info("cli channel started")

	c.wg.Add(1)
	go c.readInput(ctx)

	return nil
}

// Stop gracefully shuts down the channel.
func (c *CLIChannel) Stop() error {
	close(c.done)
	c.wg.Wait()
	close(c.messages)
	logger.Info("cli channel stopped")
	return nil
}

// Send prints a response to stdout.
func (c *CLIChannel) Send(ctx context.Context, resp *Response) error {
	fmt.Println(resp.Text)
	fmt.Println() // Empty line after response
	return nil
}

// Messages returns the incoming message channel.
func (c *CLIChannel) Messages() <-chan *Message {
	return c.messages
}

// readInput reads lines from stdin.
func (c *CLIChannel) readInput(ctx context.Context) {
	defer c.wg.Done()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
			fmt.Print(c.prompt)

			if !scanner.Scan() {
				// EOF or error
				return
			}

			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}

			// Check for exit commands
			if text == "exit" || text == "quit" || text == "/exit" || text == "/quit" {
				fmt.Println("Goodbye!")
				return
			}

			c.msgID++
			msg := &Message{
				ID:        fmt.Sprintf("cli-%d", c.msgID),
				ChannelID: "cli:local",
				UserID:    "local",
				Username:  os.Getenv("USER"),
				Text:      text,
				Metadata:  make(map[string]string),
			}

			select {
			case c.messages <- msg:
			case <-c.done:
				return
			}
		}
	}
}
