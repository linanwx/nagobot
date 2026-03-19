package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/linanwx/nagobot/channel"
	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var cliClientCmd = &cobra.Command{
	Use:   "cli",
	Short: "Connect to a running nagobot daemon",
	Long:  `Connect to a running nagobot daemon via unix socket and start an interactive chat session.`,
	RunE:  runCLIClient,
}

var cliMessageFlag string

func init() {
	rootCmd.AddCommand(cliClientCmd)
	cliClientCmd.Flags().StringVarP(&cliMessageFlag, "message", "m", "", "Send a single message and exit (one-shot mode)")
}

// socketInbound mirrors channel.socketInbound for the client side.
type socketInbound struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func runCLIClient(cmd *cobra.Command, args []string) error {
	socketPath, err := config.SocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("cannot connect to daemon at %s: %w\nIs nagobot serve running?", socketPath, err)
	}
	defer conn.Close()

	// One-shot mode: send message, wait for response, exit.
	if cliMessageFlag != "" {
		encoder := json.NewEncoder(conn)
		if err := encoder.Encode(socketInbound{Type: "message", Text: cliMessageFlag}); err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}
		decoder := json.NewDecoder(conn)
		var lastContent string
		for {
			var msg channel.SocketOutbound
			if err := decoder.Decode(&msg); err != nil {
				break
			}
			switch msg.Type {
			case "content":
				if len(msg.Text) > len(lastContent) {
					fmt.Print(msg.Text[len(lastContent):])
				}
				if msg.Final {
					fmt.Println()
					return nil
				}
				lastContent = msg.Text
			case "error":
				return fmt.Errorf("%s", msg.Text)
			}
		}
		return nil
	}

	fmt.Println("Connected to nagobot daemon. Type 'exit' to quit.")

	// Handle Ctrl-C gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	done := make(chan struct{})
	inputDone := make(chan struct{})

	// Read responses from socket.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		decoder := json.NewDecoder(conn)
		var lastContent string
		for {
			var msg channel.SocketOutbound
			if err := decoder.Decode(&msg); err != nil {
				return
			}

			switch msg.Type {
			case "content":
				// Incremental print: only print new characters.
				if len(msg.Text) > len(lastContent) {
					fmt.Print(msg.Text[len(lastContent):])
				}
				if msg.Final {
					fmt.Println()
					fmt.Println()
					lastContent = ""
					// If input is done (piped mode), exit after final response.
					select {
					case <-inputDone:
						conn.Close()
						return
					default:
						fmt.Print("nagobot> ")
					}
				} else {
					lastContent = msg.Text
				}
			case "error":
				fmt.Fprintf(os.Stderr, "\nError: %s\n", msg.Text)
				lastContent = ""
				select {
				case <-inputDone:
					conn.Close()
					return
				default:
					fmt.Print("nagobot> ")
				}
			}
		}
	}()

	// Read user input from stdin.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(inputDone)
		scanner := bufio.NewScanner(os.Stdin)
		encoder := json.NewEncoder(conn)

		fmt.Print("nagobot> ")
		for scanner.Scan() {
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				fmt.Print("nagobot> ")
				continue
			}
			if text == "exit" || text == "quit" || text == "/exit" || text == "/quit" {
				fmt.Println("Goodbye!")
				conn.Close()
				return
			}

			if err := encoder.Encode(socketInbound{Type: "message", Text: text}); err != nil {
				return
			}
		}
		// EOF (Ctrl-D or piped input finished).
		// Don't close connection — wait for pending responses via the read goroutine.
	}()

	// Wait for either signal or connection close.
	select {
	case <-sigCh:
		fmt.Println("\nGoodbye!")
		conn.Close()
	case <-done:
	}

	return nil
}
