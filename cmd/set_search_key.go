package cmd

import (
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

var setSearchKeyCmd = &cobra.Command{
	Use:     "set-search-key",
	Short:   "Manage search provider API keys",
	GroupID: "internal",
	Long: `Add, list, or remove API keys for web search providers.

Examples:
  nagobot set-search-key --provider brave --key BSA_xxx
  nagobot set-search-key --provider zhipu --key xxx
  nagobot set-search-key --list
  nagobot set-search-key --provider brave --clear`,
	RunE: runSetSearchKey,
}

var (
	searchKeyProvider string
	searchKeyValue   string
	searchKeyList    bool
	searchKeyClear   bool
)

func init() {
	setSearchKeyCmd.Flags().StringVar(&searchKeyProvider, "provider", "", "Search provider name (e.g. brave, zhipu)")
	setSearchKeyCmd.Flags().StringVar(&searchKeyValue, "key", "", "API key value")
	setSearchKeyCmd.Flags().BoolVar(&searchKeyList, "list", false, "List configured providers")
	setSearchKeyCmd.Flags().BoolVar(&searchKeyClear, "clear", false, "Remove the key for the specified provider")
	rootCmd.AddCommand(setSearchKeyCmd)
}

func runSetSearchKey(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Tools.Web.Search.Keys == nil {
		cfg.Tools.Web.Search.Keys = make(map[string]string)
	}

	// --list: show configured providers
	if searchKeyList {
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-search-key"}, {"mode", "list"},
		}, "") + "\n")
		if len(cfg.Tools.Web.Search.Keys) == 0 {
			fmt.Println("No search provider keys configured.")
			fmt.Println("Add one: nagobot set-search-key --provider brave --key <api_key>")
			return nil
		}
		fmt.Println("Configured search providers:")
		for name, key := range cfg.Tools.Web.Search.Keys {
			masked := maskKey(key)
			fmt.Printf("  %s: %s\n", name, masked)
		}
		return nil
	}

	provider := strings.TrimSpace(searchKeyProvider)
	if provider == "" {
		return fmt.Errorf("--provider is required.\nFix: nagobot set-search-key --provider <name> --key <api_key>\nSupported: brave, opensearch, zhipu")
	}

	// --clear: remove key
	if searchKeyClear {
		delete(cfg.Tools.Web.Search.Keys, provider)
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-search-key"}, {"status", "ok"}, {"provider", provider}, {"action", "cleared"},
		}, fmt.Sprintf("Removed key for provider %q.", provider)) + "\n")
		return nil
	}

	// Set key
	key := strings.TrimSpace(searchKeyValue)
	if key == "" {
		// Show status for this provider
		existing := cfg.Tools.Web.Search.Keys[provider]
		configured := existing != ""
		fmt.Print(tools.CmdOutput([][2]string{
			{"command", "set-search-key"}, {"provider", provider}, {"configured", fmt.Sprintf("%t", configured)},
		}, "") + "\n")
		if !configured {
			fmt.Printf("Provider %q: not configured\n", provider)
		} else {
			fmt.Printf("Provider %q: %s\n", provider, maskKey(existing))
		}
		return nil
	}

	cfg.Tools.Web.Search.Keys[provider] = key
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "set-search-key"}, {"status", "ok"}, {"provider", provider},
	}, fmt.Sprintf("Set key for provider %q: %s", provider, maskKey(key))) + "\n")
	return nil
}

func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return key[:4] + "****"
}
