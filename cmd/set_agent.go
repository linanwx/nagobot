package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	sessionPkg "github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/tools"
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
  nagobot set-agent --session "discord:123456" --provider openrouter --model xiaomi/mimo-v2.5-pro
  nagobot set-agent --session "discord:123456"                  # clear override`,
	RunE: runSetAgent,
}

var (
	setAgentSession  string
	setAgentName     string
	setAgentProvider string
	setAgentModel    string
)

func init() {
	setAgentCmd.Flags().StringVar(&setAgentSession, "session", "", "Session key (required)")
	setAgentCmd.Flags().StringVar(&setAgentName, "agent", "", "Agent name (empty to clear)")
	setAgentCmd.Flags().StringVar(&setAgentProvider, "provider", "", "Provider for model-pinned agent (used with --model)")
	setAgentCmd.Flags().StringVar(&setAgentModel, "model", "", "Model type — auto-creates a fixed agent (used with --provider)")
	_ = setAgentCmd.MarkFlagRequired("session")
	rootCmd.AddCommand(setAgentCmd)
}

func runSetAgent(_ *cobra.Command, _ []string) error {
	session := strings.TrimSpace(setAgentSession)
	if session == "" {
		return fmt.Errorf("--session is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	modelArg := strings.TrimSpace(setAgentModel)
	providerArg := strings.TrimSpace(setAgentProvider)
	agentArg := strings.TrimSpace(setAgentName)

	hasModel := modelArg != ""
	hasAgent := agentArg != ""
	bareClear := !hasModel && !hasAgent

	if providerArg != "" && !hasModel {
		return fmt.Errorf("--provider requires --model")
	}

	configChanged := false

	// --provider/--model mode: pin a model to this session via a session rule.
	if hasModel {
		if providerArg == "" {
			providerArg = provider.ProviderForModel(modelArg)
			if providerArg == "" {
				return fmt.Errorf("unknown model %q and no --provider specified", modelArg)
			}
		}
		if err := provider.ValidateProviderModelType(providerArg, modelArg); err != nil {
			return fmt.Errorf("invalid provider/model: %w", err)
		}
		pc := cfg.Providers.GetProviderConfig(providerArg)
		hasKey := pc != nil && strings.TrimSpace(pc.APIKey) != ""
		oauthTok := cfg.GetOAuthToken(providerArg)
		hasOAuth := oauthTok != nil && oauthTok.AccessToken != ""
		if !hasKey && !hasOAuth {
			return fmt.Errorf("provider %q has no API key configured.\nFix: nagobot set-provider-key --provider %s --api-key YOUR_KEY", providerArg, providerArg)
		}
		cfg.Thread.Models = config.UpsertModelRule(cfg.Thread.Models, config.ModelRuleSession, session, providerArg, modelArg)
		configChanged = true
	}

	// --agent mode: validate the agent exists before assigning it.
	if hasAgent {
		workspace, wErr := cfg.WorkspacePath()
		if wErr != nil {
			return fmt.Errorf("failed to get workspace: %w", wErr)
		}
		found := false
		for _, dir := range []string{"agents", "agents-builtin"} {
			path := filepath.Join(workspace, dir, agentArg+".md")
			if _, err := os.Stat(path); err == nil {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("agent %q not found in agents/ or agents-builtin/.\nTo pin a model to this session, use: nagobot set-agent --session %s --provider <name> --model <model>", agentArg, session)
		}
	}

	// Bare clear also removes any session model rule (full revert to default).
	if bareClear {
		before := len(cfg.Thread.Models)
		cfg.Thread.Models = config.RemoveModelRule(cfg.Thread.Models, config.ModelRuleSession, session)
		if len(cfg.Thread.Models) != before {
			configChanged = true
		}
	}

	if configChanged {
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
	}

	// Persist agent assignment to meta.json (per-session). The session model pin
	// lives in config (above); meta.json only carries session→agent.
	if hasAgent || bareClear {
		sessionsDir, err := cfg.SessionsDir()
		if err != nil {
			return fmt.Errorf("failed to get sessions dir: %w", err)
		}
		sessionDir := sessionPkg.SessionDir(sessionsDir, session)
		sessionPkg.UpdateMeta(sessionDir, func(m *sessionPkg.Meta) {
			if bareClear {
				m.Agent = ""
			} else if hasAgent {
				m.Agent = agentArg
			}
		})
	}

	// Output.
	if bareClear {
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-agent"}, {"status", "ok"}, {"session", session}, {"agent", "cleared"}, {"model", "cleared"},
		}, fmt.Sprintf("Cleared agent + session model rule for session %q (reverts to default).", session)) + "\n")
		return nil
	}
	if hasModel {
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-agent"}, {"status", "ok"}, {"session", session},
			{"rule", "session"}, {"provider", providerArg}, {"model", modelArg},
		}, fmt.Sprintf("Pinned session %q → %s / %s (session model rule).", session, providerArg, modelArg)) + "\n")
	}
	if hasAgent {
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-agent"}, {"status", "ok"}, {"session", session}, {"agent", agentArg},
		}, fmt.Sprintf("Set agent %q for session %q.", agentArg, session)) + "\n")
		printAgentModelRouting(cfg, agentArg)
	}
	return nil
}

func printAgentModelRouting(cfg *config.Config, agentName string) {
	// Agent rule wins; otherwise show the agent's first specialty rule (or default).
	if r := config.FindModelRule(cfg.Thread.Models, config.ModelRuleAgent, agentName); r != nil {
		fmt.Printf("Agent rule: %s -> %s / %s\n", agentName, r.Provider, r.ModelType)
		return
	}
	for _, slot := range scanAgentModelSlots() {
		if !strings.EqualFold(slot.AgentName, agentName) || slot.ModelType == "" {
			continue
		}
		prov, model := cfg.GetProvider(), cfg.GetModelType()
		if r := config.FindModelRule(cfg.Thread.Models, config.ModelRuleSpecialty, slot.ModelType); r != nil {
			prov, model = r.Provider, r.ModelType
		}
		fmt.Printf("Specialty: %s -> %s / %s\n", slot.ModelType, prov, model)
		return
	}
}
