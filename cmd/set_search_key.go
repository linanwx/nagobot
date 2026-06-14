package cmd

import (
	"fmt"
	"slices"
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
  nagobot set-search-key --provider brave --key BSA_yyy --append   # add to brave key pool
  nagobot set-search-key --provider zhipu --key xxx
  nagobot set-search-key --list
  nagobot set-search-key --provider brave --clear

For brave, the key may be a comma-separated POOL of subscription tokens across
accounts. Requests round-robin evenly across the pool so each key stays within
its own monthly free quota/credit and the per-key 1 req/s rate limit is
multiplied by the pool size. A key that returns 429/quota is briefly cooled and
skipped. Put only free/credit-bearing keys in the pool; keep pay-from-first-query
keys (e.g. Base $3/1k) out of it. Use --append to add one key at a time.`,
	RunE: runSetSearchKey,
}

var (
	searchKeyProvider string
	searchKeyValue    string
	searchKeyList     bool
	searchKeyClear    bool
	searchKeyAppend   bool
)

func init() {
	setSearchKeyCmd.Flags().StringVar(&searchKeyProvider, "provider", "", "Search provider name (e.g. brave, zhipu)")
	setSearchKeyCmd.Flags().StringVar(&searchKeyValue, "key", "", "API key value (brave: comma-separated pool allowed)")
	setSearchKeyCmd.Flags().BoolVar(&searchKeyAppend, "append", false, "Append the key to the existing pool instead of replacing (brave)")
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
			masked := maskPool(key)
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
			fmt.Printf("Provider %q: %s\n", provider, maskPool(existing))
		}
		return nil
	}

	action := "set"
	if searchKeyAppend {
		// Append to the existing comma-separated pool, de-duplicating.
		existing := splitPool(cfg.Tools.Web.Search.Keys[provider])
		for _, k := range splitPool(key) {
			if !slices.Contains(existing, k) {
				existing = append(existing, k)
			}
		}
		key = strings.Join(existing, ",")
		action = "appended"
	}

	cfg.Tools.Web.Search.Keys[provider] = key
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "set-search-key"}, {"status", "ok"}, {"provider", provider}, {"action", action},
	}, fmt.Sprintf("Provider %q now has %s", provider, maskPool(key))) + "\n")
	return nil
}

func splitPool(v string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if k := strings.TrimSpace(f); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return key[:4] + "****"
}

// maskPool masks each key in a (possibly comma-separated) pool, e.g.
// "BSA1****, BSA2**** (2 keys)".
func maskPool(v string) string {
	keys := splitPool(v)
	if len(keys) <= 1 {
		return maskKey(v)
	}
	masked := make([]string, len(keys))
	for i, k := range keys {
		masked[i] = maskKey(k)
	}
	return fmt.Sprintf("%s (%d keys)", strings.Join(masked, ", "), len(keys))
}
