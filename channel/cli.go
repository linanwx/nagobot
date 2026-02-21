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
	"golang.org/x/term"
)

const (
	cliMessageBufferSize = 10
	cliStopWaitTimeout   = 500 * time.Millisecond
)

// NewCLIChannel creates a CLI channel.
// If stdin is a terminal, it returns a TUI-based channel; otherwise a plain scanner.
func NewCLIChannel() Channel {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return newTUIChannel()
	}
	return newPlainCLIChannel()
}

// plainCLIChannel implements the Channel interface using bufio.Scanner (for non-TTY).
type plainCLIChannel struct {
	prompt       string
	messages     chan *Message
	done         chan struct{}
	responseDone chan struct{}
	wg           sync.WaitGroup
	msgID        int64
	mu           sync.Mutex
	waitingResp  bool
	stopOnce     sync.Once
}

func newPlainCLIChannel() *plainCLIChannel {
	return &plainCLIChannel{
		prompt:       "nagobot> ",
		messages:     make(chan *Message, cliMessageBufferSize),
		done:         make(chan struct{}),
		responseDone: make(chan struct{}, 1),
	}
}

func (c *plainCLIChannel) Name() string {
	return "cli"
}

func (c *plainCLIChannel) Start(ctx context.Context) error {
	logger.Info("cli channel started (plain mode)")

	c.wg.Add(1)
	go c.readInput(ctx)

	return nil
}

func (c *plainCLIChannel) Stop() error {
	c.stopOnce.Do(func() {
		close(c.done)

		waitDone := make(chan struct{})
		go func() {
			c.wg.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
			close(c.messages)
		case <-time.After(cliStopWaitTimeout):
			logger.Warn("cli channel stop timed out waiting for input loop")
		}

		logger.Info("cli channel stopped")
	})
	return nil
}

func (c *plainCLIChannel) Send(_ context.Context, resp *Response) error {
	fmt.Println()
	fmt.Println(resp.Text)
	fmt.Println()

	if c.completeWaitingResponse() {
		select {
		case c.responseDone <- struct{}{}:
		default:
		}
	} else {
		fmt.Print(c.prompt)
	}

	return nil
}

func (c *plainCLIChannel) Messages() <-chan *Message {
	return c.messages
}

func (c *plainCLIChannel) readInput(ctx context.Context) {
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
				return
			}

			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}

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

func (c *plainCLIChannel) setWaitingResponse(v bool) {
	c.mu.Lock()
	c.waitingResp = v
	c.mu.Unlock()
}

func (c *plainCLIChannel) completeWaitingResponse() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.waitingResp {
		return false
	}
	c.waitingResp = false
	return true
}
