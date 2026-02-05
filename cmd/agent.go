package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/agent"
	"github.com/linanwx/nagobot/config"
)

var (
	messageFlag string
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Chat with the nagobot agent",
	Long: `Start an interactive chat session with the nagobot agent,
or send a single message with the -m flag.

Examples:
  nagobot agent                    # Interactive mode
  nagobot agent -m "Hello world"   # Single message`,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.Flags().StringVarP(&messageFlag, "message", "m", "", "Send a single message")
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w\nRun 'nagobot onboard' to initialize", err)
	}

	// Create agent
	a, err := agent.NewAgent(cfg)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	ctx := context.Background()

	// Single message mode
	if messageFlag != "" {
		response, err := a.Run(ctx, messageFlag)
		if err != nil {
			return fmt.Errorf("agent error: %w", err)
		}
		fmt.Println(response)
		return nil
	}

	// Interactive mode
	fmt.Println("nagobot interactive mode (type 'exit' or Ctrl+C to quit)")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("you> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		response, err := a.Run(ctx, input)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Printf("\nnagobot> %s\n\n", response)
	}

	return nil
}
