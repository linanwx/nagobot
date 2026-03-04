package cmd

import (
	"fmt"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var setTelegramCmd = &cobra.Command{
	Use:     "set-telegram",
	Short:   "Manage Telegram bot configuration",
	GroupID: "internal",
	Long: `View, set, or clear the Telegram bot token and allowed user/chat IDs.

Examples:
  nagobot set-telegram                                     # show current status
  nagobot set-telegram --token BOT_TOKEN                   # set bot token
  nagobot set-telegram --allowed "123456,789012"           # set allowed IDs
  nagobot set-telegram --token BOT_TOKEN --allowed "123"   # set both
  nagobot set-telegram --clear                             # clear all Telegram config`,
	RunE: runSetTelegram,
}

var (
	setTgToken   string
	setTgAllowed string
	setTgClear   bool
)

func init() {
	setTelegramCmd.Flags().StringVar(&setTgToken, "token", "", "Telegram bot token (from @BotFather)")
	setTelegramCmd.Flags().StringVar(&setTgAllowed, "allowed", "", "Comma-separated allowed user/chat IDs (empty string to allow all)")
	setTelegramCmd.Flags().BoolVar(&setTgClear, "clear", false, "Clear all Telegram configuration")
	rootCmd.AddCommand(setTelegramCmd)
}

func runSetTelegram(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	flags := cmd.Flags()
	tokenChanged := flags.Changed("token")
	allowedChanged := flags.Changed("allowed")

	// No flags: show status
	if !tokenChanged && !allowedChanged && !setTgClear {
		return showTelegramStatus(cfg)
	}

	// --clear: remove all Telegram config
	if setTgClear {
		if cfg.Channels != nil && cfg.Channels.Telegram != nil {
			cfg.Channels.Telegram.Token = ""
			cfg.Channels.Telegram.AllowedIDs = nil
		}
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Println("Cleared all Telegram configuration")
		return nil
	}

	if cfg.Channels == nil {
		cfg.Channels = &config.ChannelsConfig{}
	}
	if cfg.Channels.Telegram == nil {
		cfg.Channels.Telegram = &config.TelegramChannelConfig{}
	}

	if tokenChanged {
		cfg.Channels.Telegram.Token = strings.TrimSpace(setTgToken)
	}
	if allowedChanged {
		cfg.Channels.Telegram.AllowedIDs = parseAllowedIDs(setTgAllowed)
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if tokenChanged {
		t := strings.TrimSpace(setTgToken)
		if t == "" {
			fmt.Println("Cleared Telegram bot token")
		} else {
			fmt.Printf("Set Telegram bot token: %s\n", maskKey(t))
		}
	}
	if allowedChanged {
		ids := parseAllowedIDs(setTgAllowed)
		if len(ids) == 0 {
			fmt.Println("Cleared allowed IDs (all users can interact)")
		} else {
			fmt.Printf("Set allowed IDs: %s\n", formatAllowedIDs(ids))
		}
	}

	return nil
}

func showTelegramStatus(cfg *config.Config) error {
	token := cfg.GetTelegramToken()
	ids := cfg.GetTelegramAllowedIDs()

	fmt.Println("Telegram configuration:")
	if token == "" {
		fmt.Println("  Token:       not configured")
	} else {
		fmt.Printf("  Token:       %s\n", maskKey(token))
	}
	if len(ids) == 0 {
		fmt.Println("  Allowed IDs: (all)")
	} else {
		fmt.Printf("  Allowed IDs: %s\n", formatAllowedIDs(ids))
	}
	return nil
}
