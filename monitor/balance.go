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

// --- OpenAI (OAuth usage from chatgpt.com/backend-api/wham/usage) ---

// OpenAIQuota queries OpenAI usage via the ChatGPT wham/usage endpoint.
type OpenAIQuota struct {
	AccessToken string
	AccountID   string
}

func (b *OpenAIQuota) Provider() string { return "openai" }
func (b *OpenAIQuota) Available() bool  { return b.AccessToken != "" }

func (b *OpenAIQuota) Check(ctx context.Context) (*BalanceInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.AccessToken)
	if b.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", b.AccountID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &BalanceInfo{Provider: "openai", Error: fmt.Sprintf("request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return &BalanceInfo{Provider: "openai", Error: "OAuth token expired (run: nagobot auth login openai)"}, nil
	}
	if resp.StatusCode != 200 {
		return &BalanceInfo{Provider: "openai", Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}, nil
	}

	var data struct {
		RateLimit *struct {
			PrimaryWindow *struct {
				LimitWindowSeconds int     `json:"limit_window_seconds"`
				UsedPercent        float64 `json:"used_percent"`
				ResetAt            int64   `json:"reset_at"`
			} `json:"primary_window"`
			SecondaryWindow *struct {
				LimitWindowSeconds int     `json:"limit_window_seconds"`
				UsedPercent        float64 `json:"used_percent"`
				ResetAt            int64   `json:"reset_at"`
			} `json:"secondary_window"`
		} `json:"rate_limit"`
		PlanType string `json:"plan_type"`
		Credits  *struct {
			Balance json.RawMessage `json:"balance"`
		} `json:"credits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return &BalanceInfo{Provider: "openai", Error: fmt.Sprintf("parse error: %v", err)}, nil
	}

	info := &BalanceInfo{Provider: "openai", Available: true}

	if data.RateLimit != nil {
		if pw := data.RateLimit.PrimaryWindow; pw != nil {
			hours := pw.LimitWindowSeconds / 3600
			if hours == 0 {
				hours = 3
			}
			detail := fmt.Sprintf("%.1f%% used", pw.UsedPercent)
			if pw.ResetAt > 0 {
				resetTime := time.Unix(pw.ResetAt, 0)
				detail += fmt.Sprintf(", resets %s", resetTime.Local().Format("15:04"))
			}
			info.Balances = append(info.Balances, BalanceEntry{
				Currency: fmt.Sprintf("%dh", hours),
				Balance:  100 - pw.UsedPercent,
				Detail:   detail,
			})
		}
		if sw := data.RateLimit.SecondaryWindow; sw != nil {
			hours := sw.LimitWindowSeconds / 3600
			label := "Day"
			if hours >= 168 {
				label = "Week"
			}
			detail := fmt.Sprintf("%.1f%% used", sw.UsedPercent)
			if sw.ResetAt > 0 {
				resetTime := time.Unix(sw.ResetAt, 0)
				detail += fmt.Sprintf(", resets %s", resetTime.Local().Format("Jan 2 15:04"))
			}
			info.Balances = append(info.Balances, BalanceEntry{
				Currency: label,
				Balance:  100 - sw.UsedPercent,
				Detail:   detail,
			})
		}
	}

	if data.Credits != nil && len(data.Credits.Balance) > 0 && string(data.Credits.Balance) != "null" {
		var bal float64
		if err := json.Unmarshal(data.Credits.Balance, &bal); err != nil {
			// Try parsing as string (OpenAI sometimes returns string instead of number).
			var balStr string
			if err2 := json.Unmarshal(data.Credits.Balance, &balStr); err2 == nil {
				fmt.Sscanf(balStr, "%f", &bal)
			}
		}
		info.Balances = append(info.Balances, BalanceEntry{
			Currency: "credits",
			Balance:  bal,
			Detail:   fmt.Sprintf("$%.2f", bal),
		})
	}

	if data.PlanType != "" {
		info.Balances = append(info.Balances, BalanceEntry{
			Currency: "plan",
			Detail:   data.PlanType,
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
