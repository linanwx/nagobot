# OpenAI OAuth Usage via wham/usage Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix OpenAI OAuth balance query by calling `https://chatgpt.com/backend-api/wham/usage` directly, instead of relying on non-existent rate-limit response headers.

**Architecture:** Replace `OpenAIQuota.Check()` to call the wham/usage endpoint with OAuth token + account ID. Display usage as window-based percentages (3h, day/week). Remove the dead-code path of persisting quota from response headers. The `Quota` struct and `extractQuota` stay in provider package since they don't hurt and Runner still references them generically.

**Tech Stack:** Go, net/http

---

## File Map

| File | Change | Responsibility |
|------|--------|---------------|
| `monitor/balance.go:156-188` | Rewrite | `OpenAIQuota` calls wham/usage endpoint |
| `monitor/quota.go` | Delete | No longer needed (was for openai_quota.json persistence) |
| `thread/run.go:72-77` | Delete | Remove `StoreQuota` call |
| `cmd/monitor.go:180-182` | Update | Pass OAuth config to `OpenAIQuota` |

---

### Task 1: Rewrite OpenAIQuota to call wham/usage

**Files:**
- Modify: `monitor/balance.go:156-188`

- [ ] **Step 1: Replace OpenAIQuota struct and Check method**

Replace lines 156-188 with:

```go
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
			Balance *float64 `json:"balance"`
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
			info.Balances = append(info.Balances, BalanceEntry{
				Currency: fmt.Sprintf("%dh", hours),
				Balance:  100 - pw.UsedPercent,
				Detail:   fmt.Sprintf("%.1f%% used", pw.UsedPercent),
			})
		}
		if sw := data.RateLimit.SecondaryWindow; sw != nil {
			hours := sw.LimitWindowSeconds / 3600
			label := "Day"
			if hours >= 168 {
				label = "Week"
			}
			info.Balances = append(info.Balances, BalanceEntry{
				Currency: label,
				Balance:  100 - sw.UsedPercent,
				Detail:   fmt.Sprintf("%.1f%% used", sw.UsedPercent),
			})
		}
	}

	if data.Credits != nil && data.Credits.Balance != nil {
		info.Balances = append(info.Balances, BalanceEntry{
			Currency: "credits",
			Balance:  *data.Credits.Balance,
			Detail:   fmt.Sprintf("$%.2f", *data.Credits.Balance),
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
```

Add `"net/http"` to the imports if not already present.

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add monitor/balance.go
git commit -m "feat: query OpenAI usage via wham/usage endpoint instead of response headers"
```

---

### Task 2: Update cmd/monitor.go to pass OAuth config

**Files:**
- Modify: `cmd/monitor.go:180-182`

- [ ] **Step 1: Replace OpenAIQuota construction**

Find where `OpenAIQuota` is constructed (around line 180). Change:

```go
&monitor.OpenAIQuota{
    MetricsDir: metricsDir,
},
```

to:

```go
func() monitor.BalanceChecker {
    cfg, _ := config.Load()
    if cfg != nil && cfg.Providers.OpenAIOAuth != nil {
        return &monitor.OpenAIQuota{
            AccessToken: cfg.Providers.OpenAIOAuth.AccessToken,
            AccountID:   cfg.Providers.OpenAIOAuth.AccountID,
        }
    }
    return &monitor.OpenAIQuota{}
}(),
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add cmd/monitor.go
git commit -m "feat: pass OAuth token to OpenAIQuota for wham/usage queries"
```

---

### Task 3: Remove dead quota persistence code

**Files:**
- Delete: `monitor/quota.go`
- Modify: `thread/run.go:72-77`

- [ ] **Step 1: Remove StoreQuota call in thread/run.go**

Delete lines 72-77:

```go
	// Persist rate-limit quota snapshot (e.g. OpenAI OAuth ratelimit headers).
	if quota != nil && cfg.MetricsStore != nil {
		if err := monitor.StoreQuota(cfg.MetricsStore.Dir(), quota); err != nil {
			logger.Warn("failed to persist quota", "err", err)
		}
	}
```

Also remove the `monitor` import from `thread/run.go` if it's no longer used.

- [ ] **Step 2: Delete monitor/quota.go**

```bash
rm monitor/quota.go
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Run tests**

Run: `go test ./... -count=1 2>&1 | tail -20`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add monitor/quota.go thread/run.go
git commit -m "cleanup: remove dead quota persistence code (openai_quota.json)"
```

---

### Task 4: Verify end-to-end

- [ ] **Step 1: Test balance query**

Run: `go run . monitor --balance`
Expected: OpenAI shows window usage percentages (e.g. `3h: X.X% used, Week: Y.Y% used`) instead of "no quota data yet".

- [ ] **Step 2: Full test suite**

Run: `go test ./... -count=1 2>&1 | tail -20`
Expected: all pass.
