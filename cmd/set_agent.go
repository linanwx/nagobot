package cmd

import (
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var setAgentCmd = &cobra.Command{
	Use:     "set-agent",
	Short:   "Set or clear the agent for a session",
	GroupID: "internal",
	Long: `Set the agent assigned to a session key in config.yaml.

The running server detects config changes automatically, so the new agent
takes effect on the next message in that session.

Examples:
  nagobot set-agent --session "discord:123456" --agent fallout
  nagobot set-agent --session "telegram:78910" --agent general
  nagobot set-agent --session "discord:123456"                  # clear override`,
	RunE: runSetAgent,
}

var (
	setAgentSession string
	setAgentName    string
)

func init() {
	setAgentCmd.Flags().StringVar(&setAgentSession, "session", "", "Session key (required)")
	setAgentCmd.Flags().StringVar(&setAgentName, "agent", "", "Agent name (empty to clear)")
	_ = setAgentCmd.MarkFlagRequired("session")
	rootCmd.AddCommand(setAgentCmd)
}

func runSetAgent(_ *cobra.Command, _ []string) error {
	session := strings.TrimSpace(setAgentSession)
	if session == "" {
		fmt.Printf("---\ncommand: set-agent\nstatus: error\n---\n\n--session is required.\nFix: nagobot set-agent --session <session_key> --agent <agent_name>\nExample: nagobot set-agent --session \"discord:123456\" --agent fallout\n")
		return fmt.Errorf("--session is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Channels == nil {
		cfg.Channels = &config.ChannelsConfig{}
	}
	if cfg.Channels.SessionAgents == nil {
		cfg.Channels.SessionAgents = make(map[string]string)
	}

	agent := strings.TrimSpace(setAgentName)
	if agent == "" {
		delete(cfg.Channels.SessionAgents, session)
	} else {
		cfg.Channels.SessionAgents[session] = agent
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if agent == "" {
		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: cleared\n---\n\nCleared agent for session %q.\n", session, session)
	} else {
		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: %s\n---\n\nSet agent %q for session %q.\n", session, agent, agent, session)
		printAgentModelRouting(cfg, agent)
	}
	return nil
}

func printAgentModelRouting(cfg *config.Config, agentName string) {
	for _, slot := range scanAgentModelSlots() {
		if !strings.EqualFold(slot.AgentName, agentName) {
			continue
		}
		prov, model := cfg.GetProvider(), cfg.GetModelType()
		if mc, ok := cfg.Thread.Models[slot.ModelType]; ok && mc != nil {
			prov, model = mc.Provider, mc.ModelType
		}
		fmt.Printf("Specialty: %s -> %s / %s\n", slot.ModelType, prov, model)
		return
	}
}
