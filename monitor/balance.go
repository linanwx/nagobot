package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// BalanceEntry holds a single currency balance.
type BalanceEntry struct {
	Currency string  `json:"currency" yaml:"currency"`
	Balance  float64 `json:"balance" yaml:"balance"`
	Limit    float64 `json:"limit,omitempty" yaml:"limit,omitempty"` // total quota (e.g. 100 for percentage-based)
	Detail   string  `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// BalanceInfo holds the balance result for a provider.
type BalanceInfo struct {
	Provider  string         `json:"provider" yaml:"provider"`
	Available bool           `json:"available" yaml:"available"`
	Balances  []BalanceEntry `json:"balances,omitempty" yaml:"balances,omitempty"`
	Error     string         `json:"error,omitempty" yaml:"error,omitempty"`
}

// IsLow returns true if any balance entry is below the low-balance threshold.
func (bi *BalanceInfo) IsLow() bool {
	if bi == nil || !bi.Available || len(bi.Balances) == 0 {
		return false
	}
	for _, b := range bi.Balances {
		switch b.Currency {
		case "CNY":
			if b.Balance > 0 && b.Balance < 5 {
				return true
			}
		case "USD", "credits":
			if b.Balance > 0 && b.Balance < 0.5 {
				return true
			}
		default:
			if b.Limit > 0 && b.Balance/b.Limit < 0.05 {
				return true
			}
		}
	}
	return false
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

// --- Moonshot ---

// MoonshotBalance checks balance via GET /v1/users/me/balance.
type MoonshotBalance struct {
	Name  string // e.g. "moonshot-cn" or "moonshot-global"
	Base  string // e.g. "https://api.moonshot.cn/v1" or "https://api.moonshot.ai/v1"
	KeyFn func() string
}

func (b *MoonshotBalance) Provider() string { return b.Name }
func (b *MoonshotBalance) Available() bool  { return b.KeyFn != nil && b.KeyFn() != "" }

func (b *MoonshotBalance) Check(ctx context.Context) (*BalanceInfo, error) {
	key := b.KeyFn()
	if key == "" {
		return &BalanceInfo{Provider: b.Name, Error: "no API key configured"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", b.Base+"/users/me/balance", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s balance request failed: %w", b.Name, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s balance: HTTP %d: %s", b.Name, resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			AvailableBalance float64 `json:"available_balance"`
			VoucherBalance   float64 `json:"voucher_balance"`
			CashBalance      float64 `json:"cash_balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("%s balance: parse error: %w", b.Name, err)
	}

	return &BalanceInfo{
		Provider:  b.Name,
		Available: true,
		Balances: []BalanceEntry{{
			Currency: "CNY",
			Balance:  result.Data.AvailableBalance,
			Detail:   fmt.Sprintf("voucher=%.2f cash=%.2f", result.Data.VoucherBalance, result.Data.CashBalance),
		}},
	}, nil
}

// --- Zhipu ---

// ZhipuBalance checks balance via the web dashboard API.
type ZhipuBalance struct {
	Name  string
	KeyFn func() string
}

func (b *ZhipuBalance) Provider() string { return b.Name }
func (b *ZhipuBalance) Available() bool  { return b.KeyFn != nil && b.KeyFn() != "" }

func (b *ZhipuBalance) Check(ctx context.Context) (*BalanceInfo, error) {
	key := b.KeyFn()
	if key == "" {
		return &BalanceInfo{Provider: b.Name, Error: "no API key configured"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://open.bigmodel.cn/api/biz/account/query-customer-account-report", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zhipu balance request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("zhipu balance: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			AvailableBalance float64 `json:"availableBalance"`
			RechargeAmount   float64 `json:"rechargeAmount"`
			GiveAmount       float64 `json:"giveAmount"`
			TotalSpendAmount float64 `json:"totalSpendAmount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("zhipu balance: parse error: %w", err)
	}
	if !result.Success {
		return &BalanceInfo{Provider: b.Name, Error: "API returned success=false"}, nil
	}

	d := result.Data
	currency := "CNY"
	if b.Name == "zhipu-global" {
		currency = "USD"
	}
	return &BalanceInfo{
		Provider:  b.Name,
		Available: true,
		Balances: []BalanceEntry{{
			Currency: currency,
			Balance:  d.AvailableBalance,
			Detail:   fmt.Sprintf("recharged=%.2f gifted=%.2f spent=%.2f", d.RechargeAmount, d.GiveAmount, d.TotalSpendAmount),
		}},
	}, nil
}

// --- Unsupported providers ---

// UnsupportedBalance reports that a provider has no balance API.
type UnsupportedBalance struct {
	Name   string
	Reason string
	KeyFn  func() string
}

func (b *UnsupportedBalance) Provider() string { return b.Name }
func (b *UnsupportedBalance) Available() bool  { return b.KeyFn != nil && b.KeyFn() != "" }

func (b *UnsupportedBalance) Check(_ context.Context) (*BalanceInfo, error) {
	return &BalanceInfo{
		Provider:  b.Name,
		Available: false,
		Error:     b.Reason,
	}, nil
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

// BalanceCache is the on-disk format for cached balance results.
type BalanceCache struct {
	Entries   []BalanceInfo `json:"entries"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// SaveBalance writes balance results to a JSON cache file.
func SaveBalance(path string, entries []BalanceInfo) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	cache := BalanceCache{
		Entries:   entries,
		UpdatedAt: time.Now(),
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal balance cache: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadBalance reads cached balance results and the last update time.
func LoadBalance(path string) ([]BalanceInfo, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	var cache BalanceCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, time.Time{}, fmt.Errorf("parse balance cache: %w", err)
	}
	return cache.Entries, cache.UpdatedAt, nil
}

// IsExhausted returns true if a provider's balance indicates it cannot serve requests.
func (bi *BalanceInfo) IsExhausted() bool {
	if bi == nil {
		return false
	}
	if bi.Error == "INSUFFICIENT CREDITS" {
		return true
	}
	hasMonetary := false
	allZero := true
	for _, b := range bi.Balances {
		switch b.Currency {
		case "req/min", "tok/min", "plan", "status":
			continue
		}
		hasMonetary = true
		if b.Balance > 0 {
			allZero = false
		}
	}
	return hasMonetary && allZero
}

// HasMonetaryBalance returns true if the balance info contains at least one
// monetary entry (not just rate limits or informational fields).
func (bi *BalanceInfo) HasMonetaryBalance() bool {
	if bi == nil {
		return false
	}
	for _, b := range bi.Balances {
		switch b.Currency {
		case "req/min", "tok/min", "plan", "status":
			continue
		}
		return true
	}
	return false
}

// IsUnreliable returns true if balance data is missing, errored, or has no monetary entries.
func (bi *BalanceInfo) IsUnreliable() bool {
	if bi == nil {
		return true
	}
	if !bi.Available && bi.Error != "" && bi.Error != "not configured" {
		return true
	}
	if !bi.HasMonetaryBalance() {
		return true
	}
	return false
}

// RunBalancePoller periodically queries all balance checkers and saves results to cachePath.
func RunBalancePoller(ctx context.Context, interval time.Duration, cachePath string, checkers []BalanceChecker) {
	// Run immediately on start.
	pollBalances(ctx, cachePath, checkers)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollBalances(ctx, cachePath, checkers)
		}
	}
}

func pollBalances(ctx context.Context, cachePath string, checkers []BalanceChecker) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	results := CheckAllBalances(queryCtx, checkers)
	if err := SaveBalance(cachePath, results); err != nil {
		logger.Warn("balance poller: failed to save cache", "err", err)
	} else {
		logger.Debug("balance poller: cache updated", "path", cachePath, "entries", len(results))
	}
}
