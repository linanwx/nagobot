package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	sessionPkg "github.com/linanwx/nagobot/session"
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
  nagobot set-agent --session "discord:123456" --provider openrouter --model xiaomi/mimo-v2-pro
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

	if providerArg != "" && modelArg == "" {
		return fmt.Errorf("--provider requires --model")
	}

	// --provider/--model mode: auto-create agent.
	if modelArg != "" {
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
		agentName, agentPath, err := createFixedAgent(cfg, providerArg, modelArg)
		if err != nil {
			return err
		}
		agentArg = agentName

		specialty := providerArg + "/" + modelArg
		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: %s\nagent_path: %s\nspecialty: %s\nprovider: %s\nmodel: %s\n---\n\n",
			session, agentName, agentPath, specialty, providerArg, modelArg)
		fmt.Printf("Created agent %q at %s\n", agentName, agentPath)
		fmt.Printf("Specialty %q → %s / %s (implicit routing)\n", specialty, providerArg, modelArg)
	}

	// Validate agent exists before writing config (--agent mode only;
	// --provider/--model mode already created the file above).
	if agentArg != "" && modelArg == "" {
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
			return fmt.Errorf("agent %q not found in agents/ or agents-builtin/.\nTo create a model-pinned agent, use: nagobot set-agent --session %s --provider <name> --model <model>", agentArg, session)
		}
	}

	// Persist agent assignment to meta.json (per-session, survives restarts).
	sessionsDir, err := cfg.SessionsDir()
	if err != nil {
		return fmt.Errorf("failed to get sessions dir: %w", err)
	}
	sessionDir := sessionPkg.SessionDir(sessionsDir, session)
	sessionPkg.UpdateMeta(sessionDir, func(m *sessionPkg.Meta) {
		if agentArg == "" && modelArg == "" {
			m.Agent = ""
		} else {
			m.Agent = agentArg
		}
	})

	if agentArg == "" && modelArg == "" {
		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: cleared\n---\n\nCleared agent for session %q.\n", session, session)
	} else if modelArg == "" {
		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: %s\n---\n\nSet agent %q for session %q.\n", session, agentArg, agentArg, session)
		printAgentModelRouting(cfg, agentArg)
	} else {
		fmt.Printf("Set session %q → agent %q\n", session, agentArg)
	}
	return nil
}

func createFixedAgent(cfg *config.Config, provName, modelType string) (name, path string, err error) {
	// Specialty = "provider/model" for unambiguous routing.
	specialty := provName + "/" + modelType
	slug := strings.ReplaceAll(specialty, "/", "-")
	name = "fixed-to-" + slug

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return "", "", fmt.Errorf("failed to get workspace: %w", err)
	}
	path = filepath.Join(workspace, "agents", name+".md")

	content := fmt.Sprintf(`---
name: %s
specialty: %s
sections:
  - user_memory_section
  - heartbeat_prompt_section
---
You are a member of the nagobot family. You are a helpful assistant.
`, name, specialty)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", "", fmt.Errorf("failed to create agents dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write agent template: %w", err)
	}
	return name, path, nil
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
