package channel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const (
	cliMessageBufferSize = 10
	cliStopWaitTimeout   = 500 * time.Millisecond
)

// CLIChannel implements the Channel interface for interactive CLI.
type CLIChannel struct {
	prompt       string
	messages     chan *Message
	done         chan struct{}
	responseDone chan struct{}
	wg           sync.WaitGroup
	msgID        int64
	mu           sync.Mutex
	waitingResp  bool
}

// NewCLIChannel creates a new CLI channel.
func NewCLIChannel() Channel {
	return &CLIChannel{
		prompt:       "nagobot> ",
		messages:     make(chan *Message, cliMessageBufferSize),
		done:         make(chan struct{}),
		responseDone: make(chan struct{}, 1),
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
	select {
	case <-c.done:
	default:
		close(c.done)
	}

	waitDone := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		close(c.messages)
	case <-time.After(cliStopWaitTimeout):
		// stdin reads can block indefinitely on some terminals; don't block process shutdown.
		logger.Warn("cli channel stop timed out waiting for input loop")
	}

	logger.Info("cli channel stopped")
	return nil
}

// Send prints a response to stdout.
func (c *CLIChannel) Send(ctx context.Context, resp *Response) error {
	// Keep output visually separate from any already-printed prompt.
	fmt.Println()
	fmt.Println(resp.Text)
	fmt.Println() // Empty line after response

	if c.completeWaitingResponse() {
		select {
		case c.responseDone <- struct{}{}:
		default:
		}
	} else {
		// Preserve a visible prompt for out-of-band notifications.
		fmt.Print(c.prompt)
	}

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

			// Start a request/response turn, so next prompt appears after reply.
			select {
			case <-c.responseDone:
			default:
			}
			c.setWaitingResponse(true)

			select {
			case c.messages <- msg:
			case <-c.done:
				c.setWaitingResponse(false)
				return
			case <-ctx.Done():
				c.setWaitingResponse(false)
				return
			}

			select {
			case <-c.responseDone:
			case <-c.done:
				c.setWaitingResponse(false)
				return
			case <-ctx.Done():
				c.setWaitingResponse(false)
				return
			}
		}
	}
}

func (c *CLIChannel) setWaitingResponse(v bool) {
	c.mu.Lock()
	c.waitingResp = v
	c.mu.Unlock()
}

func (c *CLIChannel) completeWaitingResponse() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.waitingResp {
		return false
	}
	c.waitingResp = false
	return true
}
