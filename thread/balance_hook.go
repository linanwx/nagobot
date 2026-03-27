package thread

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/monitor"
	"github.com/linanwx/nagobot/provider"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
)

// balanceWarningHook returns a turnHook that injects a low-balance warning
// when the current provider's cached balance is below threshold.
func (t *Thread) balanceWarningHook() turnHook {
	return func(ctx context.Context, tc turnContext) []string {
		if !sysmsg.IsUserVisibleSource(t.lastWakeSource) {
			return nil
		}

		cfg := t.cfg()
		cachePath := filepath.Join(cfg.Workspace, "system", "balance-cache.json")
		entries, _, err := monitor.LoadBalance(cachePath)
		if err != nil || len(entries) == 0 {
			return nil
		}

		provName, _ := t.resolvedProviderModel()
		if provName == "" {
			return nil
		}

		var current *monitor.BalanceInfo
		for i := range entries {
			if entries[i].Provider == provName {
				current = &entries[i]
				break
			}
		}
		if current == nil || !current.IsLow() {
			return nil
		}

		// Build fallback list from cached balances.
		var alternatives []string
		for _, bi := range entries {
			if bi.Provider == provName || !bi.Available || bi.IsLow() {
				continue
			}
			models := provider.SupportedModelsForProvider(bi.Provider)
			if len(models) == 0 {
				continue
			}
			var balDetail string
			for _, b := range bi.Balances {
				if b.Detail != "" {
					balDetail = b.Detail
					break
				}
			}
			line := fmt.Sprintf("- %s: %s", bi.Provider, strings.Join(models, ", "))
			if balDetail != "" {
				line += " (" + balDetail + ")"
			}
			alternatives = append(alternatives, line)
		}

		// Current balance detail.
		var currentDetail string
		for _, b := range current.Balances {
			if b.Detail != "" {
				currentDetail = b.Detail
				break
			}
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("⚠ Provider %s balance is low", provName))
		if currentDetail != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", currentDetail))
		}
		sb.WriteString(".\n")
		if len(alternatives) > 0 {
			sb.WriteString("Available alternatives:\n")
			for _, alt := range alternatives {
				sb.WriteString(alt + "\n")
			}
			sb.WriteString("Suggest the user switch. Use the set-model skill to apply changes.")
		} else {
			sb.WriteString("No alternative providers with sufficient balance found.")
		}

		logger.Info("balance warning injected",
			"sessionKey", tc.SessionKey,
			"provider", provName,
			"detail", currentDetail,
		)

		return []string{sb.String()}
	}
}
