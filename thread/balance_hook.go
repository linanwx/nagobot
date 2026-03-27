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

		// Index by provider.
		balanceMap := make(map[string]*monitor.BalanceInfo, len(entries))
		for i := range entries {
			balanceMap[entries[i].Provider] = &entries[i]
		}

		current := balanceMap[provName]
		if current == nil || !current.IsLow() {
			return nil
		}

		// Classify all configured providers using the same logic as set-model --list-fallback.
		var available, exhausted, unreliable []fallbackEntry
		for _, prov := range provider.SupportedProviders() {
			if prov == provName {
				continue
			}
			models := provider.SupportedModelsForProvider(prov)
			if len(models) == 0 {
				continue
			}
			bi := balanceMap[prov]
			entry := fallbackEntry{name: prov, models: models, balance: bi}
			if bi == nil || bi.IsUnreliable() {
				unreliable = append(unreliable, entry)
			} else if bi.IsExhausted() {
				exhausted = append(exhausted, entry)
			} else {
				available = append(available, entry)
			}
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("⚠ Provider %s balance is low (%s).\n", provName, balanceSummary(current)))

		if len(available) > 0 {
			sb.WriteString("\nAvailable alternatives:\n")
			for _, a := range available {
				sb.WriteString(fmt.Sprintf("  %s: %s", a.name, strings.Join(a.models, ", ")))
				if a.balance != nil {
					sb.WriteString(fmt.Sprintf(" [%s]", balanceSummary(a.balance)))
				}
				sb.WriteString("\n")
			}
		}
		if len(exhausted) > 0 {
			sb.WriteString("\nBalance exhausted:\n")
			for _, e := range exhausted {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", e.name, strings.Join(e.models, ", ")))
			}
		}
		if len(unreliable) > 0 {
			sb.WriteString("\nUnreliable (cannot verify balance):\n")
			for _, u := range unreliable {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", u.name, strings.Join(u.models, ", ")))
			}
		}

		sb.WriteString("\nSuggest the user switch affected model routing. Use the set-model skill to apply changes.")

		logger.Info("balance warning injected",
			"sessionKey", tc.SessionKey,
			"provider", provName,
		)

		return []string{sb.String()}
	}
}

type fallbackEntry struct {
	name    string
	models  []string
	balance *monitor.BalanceInfo
}

// balanceSummary returns a short human-readable summary of balance entries.
func balanceSummary(bi *monitor.BalanceInfo) string {
	if bi == nil {
		return "unknown"
	}
	var parts []string
	for _, b := range bi.Balances {
		if b.Detail != "" {
			parts = append(parts, b.Detail)
		}
	}
	if len(parts) == 0 {
		return "no details"
	}
	return strings.Join(parts, "; ")
}
