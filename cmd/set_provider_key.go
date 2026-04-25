package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/linanwx/nagobot/tools"
)

var setProviderKeyCmd = &cobra.Command{
	Use:     "set-provider-key",
	Short:   "Manage LLM provider API keys",
	GroupID: "internal",
	Long: `Add, list, or remove API keys for LLM providers.

Examples:
  nagobot set-provider-key --provider openai --api-key sk-xxx
  nagobot set-provider-key --provider anthropic --api-key sk-xxx --api-base https://custom.endpoint
  nagobot set-provider-key --list
  nagobot set-provider-key --provider openai           # show status
  nagobot set-provider-key --provider openai --clear`,
	RunE: runSetProviderKey,
}

var (
	provKeyProvider string
	provKeyAPIKey   string
	provKeyAPIBase  string
	provKeyList     bool
	provKeyClear    bool
)

func init() {
	setProviderKeyCmd.Flags().StringVar(&provKeyProvider, "provider", "", "Provider name (e.g. openai, anthropic, deepseek)")
	setProviderKeyCmd.Flags().StringVar(&provKeyAPIKey, "api-key", "", "API key value")
	setProviderKeyCmd.Flags().StringVar(&provKeyAPIBase, "api-base", "", "Custom API base URL (optional)")
	setProviderKeyCmd.Flags().BoolVar(&provKeyList, "list", false, "List all providers and their key status")
	setProviderKeyCmd.Flags().BoolVar(&provKeyClear, "clear", false, "Remove key for the specified provider")
	rootCmd.AddCommand(setProviderKeyCmd)
}

func runSetProviderKey(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	supported := provider.SupportedProviders()

	// --list: show all providers and key status
	if provKeyList {
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-provider-key"}, {"mode", "list"},
		}, "LLM provider key status:") + "\n")
		for _, name := range supported {
			pc := cfg.Providers.GetProviderConfig(name)
			status := "not configured"
			if pc != nil && strings.TrimSpace(pc.APIKey) != "" {
				status = maskKey(pc.APIKey)
			}
			// Show OAuth status only for OAuth-variant providers.
			if strings.HasSuffix(name, "-oauth") {
				if tok := cfg.GetOAuthToken(name); tok != nil && tok.AccessToken != "" {
					oauthStatus := "oauth"
					if tok.ExpiresAt > 0 {
						remaining := tok.ExpiresAt - time.Now().Unix()
						if remaining <= 0 {
							if tok.RefreshToken != "" {
								oauthStatus = "oauth (expired, has refresh token)"
							} else {
								oauthStatus = "oauth (expired)"
							}
						} else {
							days := remaining / 86400
							if days > 0 {
								oauthStatus = fmt.Sprintf("oauth (expires in %dd)", days)
							} else {
								hours := remaining / 3600
								oauthStatus = fmt.Sprintf("oauth (expires in %dh)", hours)
							}
						}
					}
					if status == "not configured" {
						status = oauthStatus
					} else {
						status += " + " + oauthStatus
					}
				}
			}
			marker := "  "
			if name == cfg.GetProvider() {
				marker = "* "
			}
			fmt.Printf("  %s%-16s %s\n", marker, name, status)
		}
		fmt.Println("\n  * = current default provider")
		return nil
	}

	provName := strings.TrimSpace(provKeyProvider)
	if provName == "" {
		return fmt.Errorf("--provider is required.\nFix: nagobot set-provider-key --provider <name> --api-key YOUR_KEY\nSupported providers: %s", strings.Join(supported, ", "))
	}

	if !isSupported(provName, supported) {
		return fmt.Errorf("unknown provider %q.\nSupported providers: %s\nFix: nagobot set-provider-key --provider <name> --api-key YOUR_KEY", provName, strings.Join(supported, ", "))
	}

	// --clear: remove key
	if provKeyClear {
		pc := cfg.Providers.GetProviderConfig(provName)
		if pc == nil {
			fmt.Printf("Provider %q has no API key config to clear.\n", provName)
			return nil
		}
		pc.APIKey = ""
		pc.APIBase = ""
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-provider-key"}, {"status", "ok"}, {"provider", provName}, {"action", "cleared"},
		}, fmt.Sprintf("Cleared API key for provider %q.", provName)) + "\n")
		return nil
	}

	apiKey := strings.TrimSpace(provKeyAPIKey)
	if apiKey == "" {
		// Show status for this provider
		pc := cfg.Providers.GetProviderConfig(provName)
		hasKey := pc != nil && strings.TrimSpace(pc.APIKey) != ""
		tok := cfg.GetOAuthToken(provName)
		hasOAuth := tok != nil && tok.AccessToken != ""
		configured := hasKey || hasOAuth
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-provider-key"}, {"provider", provName}, {"configured", fmt.Sprintf("%t", configured)},
		}, "") + "\n")
		if !configured {
			fmt.Printf("Provider %q: not configured\n", provName)
			fmt.Printf("Fix: nagobot set-provider-key --provider %s --api-key YOUR_KEY\n", provName)
		} else {
			if hasKey {
				fmt.Printf("Provider %q: %s\n", provName, maskKey(pc.APIKey))
			}
			if hasOAuth {
				fmt.Printf("Provider %q: oauth configured\n", provName)
			}
			if pc != nil && pc.APIBase != "" {
				fmt.Printf("  API base: %s\n", pc.APIBase)
			}
		}
		return nil
	}

	// Set key (and optionally base)
	pc := cfg.EnsureProviderConfigFor(provName)
	if pc == nil {
		return fmt.Errorf("provider %q does not support API key configuration", provName)
	}
	pc.APIKey = apiKey
	if provKeyAPIBase != "" {
		pc.APIBase = strings.TrimSpace(provKeyAPIBase)
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "set-provider-key"}, {"status", "ok"}, {"provider", provName},
	}, fmt.Sprintf("Set API key for provider %q: %s", provName, maskKey(apiKey))) + "\n")
	if provKeyAPIBase != "" {
		fmt.Printf("  API base: %s\n", strings.TrimSpace(provKeyAPIBase))
	}
	return nil
}

func isSupported(name string, supported []string) bool {
	for _, s := range supported {
		if s == name {
			return true
		}
	}
	return false
}
