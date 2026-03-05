package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BalanceEntry holds a single currency balance.
type BalanceEntry struct {
	Currency string  `json:"currency" yaml:"currency"`
	Balance  float64 `json:"balance" yaml:"balance"`
	Detail   string  `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// BalanceInfo holds the balance result for a provider.
type BalanceInfo struct {
	Provider  string         `json:"provider" yaml:"provider"`
	Available bool           `json:"available" yaml:"available"`
	Balances  []BalanceEntry `json:"balances,omitempty" yaml:"balances,omitempty"`
	Error     string         `json:"error,omitempty" yaml:"error,omitempty"`
}

// BalanceChecker checks account balance for a provider.
type BalanceChecker interface {
	Provider() string
	Available() bool
	Check(ctx context.Context) (*BalanceInfo, error)
}

// --- OpenRouter ---

// OpenRouterBalance checks credits via GET /api/v1/credits.
type OpenRouterBalance struct {
	KeyFn func() string
}

func (b *OpenRouterBalance) Provider() string { return "openrouter" }
func (b *OpenRouterBalance) Available() bool  { return b.KeyFn != nil && b.KeyFn() != "" }

func (b *OpenRouterBalance) Check(ctx context.Context) (*BalanceInfo, error) {
	key := b.KeyFn()
	if key == "" {
		return &BalanceInfo{Provider: "openrouter", Error: "no API key configured"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://openrouter.ai/api/v1/credits", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter credits request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openrouter credits: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("openrouter credits: parse error: %w", err)
	}

	remaining := result.Data.TotalCredits - result.Data.TotalUsage
	return &BalanceInfo{
		Provider:  "openrouter",
		Available: true,
		Balances: []BalanceEntry{{
			Currency: "USD",
			Balance:  remaining,
			Detail:   fmt.Sprintf("credits: %.4f, usage: %.4f", result.Data.TotalCredits, result.Data.TotalUsage),
		}},
	}, nil
}

// --- DeepSeek ---

// DeepSeekBalance checks balance via GET /user/balance.
type DeepSeekBalance struct {
	KeyFn func() string
}

func (b *DeepSeekBalance) Provider() string { return "deepseek" }
func (b *DeepSeekBalance) Available() bool  { return b.KeyFn != nil && b.KeyFn() != "" }

func (b *DeepSeekBalance) Check(ctx context.Context) (*BalanceInfo, error) {
	key := b.KeyFn()
	if key == "" {
		return &BalanceInfo{Provider: "deepseek", Error: "no API key configured"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.deepseek.com/user/balance", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepseek balance request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("deepseek balance: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency       string `json:"currency"`
			TotalBalance   string `json:"total_balance"`
			GrantedBalance string `json:"granted_balance"`
			ToppedUp       string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("deepseek balance: parse error: %w", err)
	}

	info := &BalanceInfo{
		Provider:  "deepseek",
		Available: result.IsAvailable,
	}

	for _, bi := range result.BalanceInfos {
		var total float64
		fmt.Sscanf(bi.TotalBalance, "%f", &total)
		info.Balances = append(info.Balances, BalanceEntry{
			Currency: bi.Currency,
			Balance:  total,
			Detail:   fmt.Sprintf("granted=%s topped_up=%s", bi.GrantedBalance, bi.ToppedUp),
		})
	}

	return info, nil
}

// CheckAllBalances queries all available balance checkers.
func CheckAllBalances(ctx context.Context, checkers []BalanceChecker) []BalanceInfo {
	var results []BalanceInfo
	for _, c := range checkers {
		if !c.Available() {
			results = append(results, BalanceInfo{
				Provider: c.Provider(),
				Error:    "not configured",
			})
			continue
		}
		info, err := c.Check(ctx)
		if err != nil {
			results = append(results, BalanceInfo{
				Provider: c.Provider(),
				Error:    fmt.Sprintf("error: %v", err),
			})
			continue
		}
		results = append(results, *info)
	}
	return results
}
