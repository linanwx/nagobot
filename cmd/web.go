package cmd

import (
	"fmt"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var webAddrOnly bool

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Open the Web Dashboard in the default browser",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		addr := cfg.GetWebAddr()
		if addr == "" {
			return fmt.Errorf("web channel address not configured")
		}
		url := "http://" + addr

		if webAddrOnly {
			fmt.Println(url)
			return nil
		}

		fmt.Printf("Opening %s ...\n", url)
		if err := openBrowser(url); err != nil {
			fmt.Println("Could not open browser automatically.")
			fmt.Printf("Please open this URL in your browser:\n\n  %s\n", url)
		}
		return nil
	},
}

func init() {
	webCmd.Flags().BoolVar(&webAddrOnly, "addr", false, "Print the Dashboard URL without opening the browser")
	rootCmd.AddCommand(webCmd)
}
