# Heartbeat Self-Scheduling Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the external poll-driven heartbeat dispatcher (LLM cron every 30 min) with a code-level self-scheduling heartbeat that cannot break, reduces LLM calls by ~80%, and adapts timing to item urgency.

**Architecture:** A Go goroutine in `cmd/serve.go` scans sessions and fires heartbeat pulses directly into user threads. Each pulse loads a single `heartbeat-wake` skill that decides whether to reflect (update heartbeat.md) or act (evaluate items and optionally message user). The heartbeat is always-on — the LLM can postpone it but never break the chain. `sleep_thread` is overridden in heartbeat context to be a pure terminate+suppress call with no scheduling.

**Tech Stack:** Go, Markdown (skills)

---

## File Map

| File | Change | Responsibility |
|------|--------|---------------|
| `thread/msg/msg.go` | Modify | Replace `WakeHeartbeatReflect`+`WakeHeartbeatWake` with single `WakeHeartbeat` |
| `thread/types.go` | Modify | Re-export new `WakeHeartbeat` constant |
| `thread/wake.go` | Modify | Update `wakeActionHint`, `messageVisibility`, suppress sink for `WakeHeartbeat` source removed (LLM calls sleep_thread) |
| `thread/compress.go` | Modify | Update `markHeartbeatTurns` and `isHeartbeatSkipTurn` for new source + trim criteria |
| `tools/sleep.go` | Modify | Add heartbeat mode: suppress + halt, no scheduling, no params |
| `cmd/heartbeat_scheduler.go` | Create | Heartbeat scheduler goroutine — scans sessions, fires pulses |
| `cmd/serve.go` | Modify | Start scheduler goroutine, remove old heartbeat RPC handlers |
| `cmd/heartbeat.go` | Modify | Replace `reflect`/`wake` subcommands with `trigger`+`postpone`; remove `heartbeat-state.json` |
| `cmd/templates/skills/heartbeat-wake/SKILL.md` | Rewrite | Single entry point: decide reflect vs act |
| `cmd/templates/skills/heartbeat-reflect/SKILL.md` | Modify | Add mandatory `sleep_thread()` at end |
| `cmd/templates/skills/heartbeat-dispatcher/SKILL.md` | Delete | Dispatcher eliminated |
| `cmd/templates/agents/heartbeat.md` | Delete | No more dispatcher agent |
| `config/defaults.go` | Modify | Remove heartbeat cron seed |
| `cmd/list_heartbeat.go` | Delete | Scheduler replaces this |
| `cmd/templates/skills/thread-ops/SKILL.md` | Modify | Add heartbeat mode note to sleep_thread docs |
| `cmd/templates/system/CORE_MECHANISM.md` | Modify | Remove "heartbeat checks" agent reference |
| `thread/compress_test.go` | Modify | Update heartbeat test fixtures for new source string |

---

### Task 1: New WakeSource `WakeHeartbeat`

**Files:**
- Modify: `thread/msg/msg.go:121-137`
- Modify: `thread/types.go:28-44`
- Modify: `thread/wake.go:128-131,267-295`

- [ ] **Step 1: Replace wake sources in msg.go**

In `thread/msg/msg.go`, replace:
```go
WakeHeartbeatReflect WakeSource = "heartbeat_reflect"
WakeHeartbeatWake    WakeSource = "heartbeat_wake"
```
with:
```go
WakeHeartbeat WakeSource = "heartbeat"
```

- [ ] **Step 2: Update types.go re-export**

In `thread/types.go`, replace:
```go
WakeHeartbeatReflect = msg.WakeHeartbeatReflect
WakeHeartbeatWake    = msg.WakeHeartbeatWake
```
with:
```go
WakeHeartbeat = msg.WakeHeartbeat
```

- [ ] **Step 3: Remove auto-suppress in RunOnce**

In `thread/wake.go:128-131`, remove:
```go
// Heartbeat reflection is always silent — suppress sink delivery.
if msg.Source == WakeHeartbeatReflect {
    t.SetSuppressSink()
}
```

Suppress is now the LLM's responsibility via `sleep_thread()`.

- [ ] **Step 4: Update wakeActionHint**

In `thread/wake.go:wakeActionHint`, replace the two heartbeat cases:
```go
case WakeHeartbeatReflect:
    return "Heartbeat reflection triggered. Load the specified skill and follow its instructions to review this session and update heartbeat.md."
case WakeHeartbeatWake:
    return "Heartbeat wake triggered. Load the specified skill and follow its instructions to check heartbeat.md and act on relevant items."
```
with:
```go
case WakeHeartbeat:
    return "Heartbeat pulse. Load the heartbeat-wake skill and follow its instructions."
```

- [ ] **Step 5: Fix all compile errors from the rename**

Run: `grep -rn 'WakeHeartbeatReflect\|WakeHeartbeatWake' --include='*.go' .`

Update every reference to use `WakeHeartbeat`. Key locations:
- `thread/compress.go` — both `markHeartbeatTurns` (lines 259, 273, 276) AND `computeToolCompressed` (line 355)
- `thread/compress_test.go` — update test fixtures using `"heartbeat_wake"` or `"heartbeat_reflect"` strings to `"heartbeat"`
- `cmd/serve.go` (RPC handlers — will be removed in Task 5)
- `cmd/heartbeat.go` (reflect/wake instructions — will be rewritten in Task 5)

For now, update compress.go and compress_test.go. Temporarily stub serve.go and heartbeat.go references to compile (they'll be fully rewritten in Task 5).

- [ ] **Step 6: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 7: Commit**

```bash
git add thread/msg/msg.go thread/types.go thread/wake.go thread/compress.go thread/compress_test.go cmd/serve.go cmd/heartbeat.go
git commit -m "refactor: replace WakeHeartbeatReflect/Wake with single WakeHeartbeat source"
```

---

### Task 2: sleep_thread heartbeat mode

**Files:**
- Modify: `tools/sleep.go`
- Modify: `thread/sleep.go`

- [ ] **Step 1: Add IsHeartbeatMode to ThreadSleeper interface**

In `tools/sleep.go`, add to the `ThreadSleeper` interface:
```go
type ThreadSleeper interface {
    SleepThread(duration time.Duration, message string) error
    SetSuppressSink()
    SetHaltLoop()
    IsHeartbeatMode() bool
}
```

- [ ] **Step 2: Implement IsHeartbeatMode on Thread**

In `thread/sleep.go`, add:
```go
// IsHeartbeatMode returns true if the current turn was triggered by a heartbeat pulse.
func (t *Thread) IsHeartbeatMode() bool {
	return t.lastWakeSource == WakeHeartbeat
}
```

- [ ] **Step 3: Override sleep_thread behavior in heartbeat mode**

In `tools/sleep.go:Run`, add at the top of the function (after arg parsing):
```go
if t.sleeper.IsHeartbeatMode() {
    t.sleeper.SetSuppressSink()
    t.sleeper.SetHaltLoop()
    return toolResult("sleep_thread", map[string]any{
        "mode": "heartbeat_terminate",
    }, "Heartbeat turn terminated. Output suppressed. "+
        "The heartbeat scheduler will fire the next pulse automatically.")
}
```

This goes before the existing `if a.Skip {` block.

- [ ] **Step 4: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add tools/sleep.go thread/sleep.go
git commit -m "feat: sleep_thread heartbeat mode — terminate+suppress, no scheduling"
```

---

### Task 3: Update heartbeat trim logic

**Files:**
- Modify: `thread/compress.go:249-306`

- [ ] **Step 1: Update markHeartbeatTurns source check**

In `thread/compress.go:markHeartbeatTurns`, replace the source check:
```go
if source != string(WakeHeartbeatReflect) && source != string(WakeHeartbeatWake) {
```
with:
```go
if source != string(WakeHeartbeat) {
```

- [ ] **Step 2: Update trim decision logic**

Replace the `shouldTrim` switch block (lines 270-281):
```go
shouldTrim := false
trimType := ""
switch source {
case string(WakeHeartbeatReflect):
    shouldTrim = true
    trimType = "reflect"
case string(WakeHeartbeatWake):
    if isHeartbeatSkipTurn(messages[i:turnEnd]) {
        shouldTrim = true
        trimType = "skip"
    }
}
```
with:
```go
shouldTrim := isHeartbeatSkipTurn(messages[i:turnEnd])
trimType := "heartbeat"
```

- [ ] **Step 3: Update isHeartbeatSkipTurn to use safe-tools criterion**

Replace `isHeartbeatSkipTurn` (lines 309-328):
```go
// isHeartbeatSkipTurn returns true if a heartbeat turn only called safe tools
// (read, use_skill, sleep_thread) and produced no real action.
func isHeartbeatSkipTurn(turnMessages []provider.Message) bool {
	for i := range turnMessages {
		m := &turnMessages[i]
		if m.Role != "tool" {
			continue
		}
		if !heartbeatSafeTools[m.Name] {
			return false
		}
	}
	return true
}
```

Update `heartbeatSafeTools` map to include: `read_file`, `use_skill`, `sleep_thread`, `write_file`. The `write_file` is needed because reflect turns write heartbeat.md — this is internal bookkeeping, not user-visible action, and must be trimmed.

- [ ] **Step 4: Verify and commit**

```bash
go build ./... && go test ./thread/... -count=1
git add thread/compress.go
git commit -m "refactor: heartbeat trim uses safe-tools criterion with single WakeHeartbeat source"
```

---

### Task 4: Heartbeat scheduler

**Files:**
- Create: `cmd/heartbeat_scheduler.go`
- Modify: `cmd/serve.go`

- [ ] **Step 1: Create heartbeat_scheduler.go**

Key design decisions from review:
- **No `session_info.json`** (doesn't exist). Use `ListThreads()` for in-memory threads (exposes `LastUserActiveAt` via ThreadInfo). For GC'd sessions, use `session.jsonl` mtime as fallback.
- **No mtime goroutine** (fragile). Check mtime at each scan cycle by comparing with stored `mdMtimeBefore`.
- **`sessionKeyToDir` defined locally** (the version in `list_heartbeat.go` gets deleted).

```go
package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/thread"
	sysmsg "github.com/linanwx/nagobot/thread/msg"
)

const (
	heartbeatQuietBefore     = 10 * time.Minute
	heartbeatDefaultInterval = 30 * time.Minute
	heartbeatFastInterval    = 10 * time.Minute // After heartbeat.md modification
	heartbeatInactiveMax     = 48 * time.Hour
	heartbeatScanInterval    = 30 * time.Second
)

type heartbeatSessionState struct {
	lastPulseFired time.Time
	mdMtimeBefore  time.Time // heartbeat.md mtime recorded before last pulse
	mdModified     bool      // true if heartbeat.md was modified during last pulse
}

type heartbeatScheduler struct {
	mgr         *thread.Manager
	cfg         func() *config.Config
	sessionsDir string

	mu       sync.Mutex
	sessions map[string]*heartbeatSessionState
}

func newHeartbeatScheduler(mgr *thread.Manager, cfgFn func() *config.Config, sessionsDir string) *heartbeatScheduler {
	return &heartbeatScheduler{
		mgr:         mgr,
		cfg:         cfgFn,
		sessionsDir: sessionsDir,
		sessions:    make(map[string]*heartbeatSessionState),
	}
}

func (hs *heartbeatScheduler) run(ctx context.Context) {
	ticker := time.NewTicker(heartbeatScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hs.scan()
		}
	}
}

func (hs *heartbeatScheduler) scan() {
	now := time.Now()
	postponed := hs.loadPostponeConfig()

	// Phase 1: in-memory threads (have accurate lastUserActiveAt).
	for _, info := range hs.mgr.ListThreads() {
		key := info.SessionKey
		if strings.HasPrefix(key, "cron:") || key == "cli" {
			continue
		}
		if info.State == "running" {
			continue
		}
		hs.maybeFirePulse(key, now, info.LastUserActiveAt, postponed)
	}

	// Phase 2: GC'd sessions on disk (thread not in memory).
	hs.scanSessionDirs(now, postponed)
}

func (hs *heartbeatScheduler) maybeFirePulse(sessionKey string, now time.Time, lastUserActive time.Time, postponed map[string]time.Time) {
	if until, ok := postponed[sessionKey]; ok && now.Before(until) {
		return
	}
	if lastUserActive.IsZero() {
		return
	}
	if now.Sub(lastUserActive) > heartbeatInactiveMax {
		return
	}
	if now.Sub(lastUserActive) < heartbeatQuietBefore {
		return
	}

	sessionDir := hbSessionKeyToDir(hs.sessionsDir, sessionKey)

	hs.mu.Lock()
	state, ok := hs.sessions[sessionKey]
	if !ok {
		state = &heartbeatSessionState{}
		hs.sessions[sessionKey] = state
	}

	// Check mtime change from previous pulse (replaces goroutine approach).
	mdPath := filepath.Join(sessionDir, "heartbeat.md")
	currentMtime := fileMtime(mdPath)
	if !state.mdMtimeBefore.IsZero() && !currentMtime.IsZero() && currentMtime.After(state.mdMtimeBefore) {
		state.mdModified = true
	}

	interval := heartbeatDefaultInterval
	if state.mdModified {
		interval = heartbeatFastInterval
		state.mdModified = false
	}

	if !state.lastPulseFired.IsZero() && now.Sub(state.lastPulseFired) < interval {
		hs.mu.Unlock()
		return
	}

	state.mdMtimeBefore = currentMtime
	state.lastPulseFired = now
	hs.mu.Unlock()

	// Build wake message.
	content := ""
	if data, err := os.ReadFile(mdPath); err == nil {
		content = strings.TrimSpace(string(data))
	}
	nextPulse := now.Add(heartbeatDefaultInterval)
	mdModifiedStr := ""
	if !currentMtime.IsZero() {
		mdModifiedStr = currentMtime.Format(time.RFC3339)
	}

	body := buildHeartbeatMessage(content, mdModifiedStr, nextPulse.Format(time.RFC3339))
	hs.mgr.Wake(sessionKey, &thread.WakeMessage{
		Source:  thread.WakeHeartbeat,
		Message: body,
	})
	logger.Debug("heartbeat pulse fired", "session", sessionKey)
}

func buildHeartbeatMessage(heartbeatContent, mdModified, nextPulse string) string {
	fields := map[string]string{
		"next_pulse": nextPulse,
	}
	if mdModified != "" {
		fields["heartbeat_modified"] = mdModified
	}
	body := "heartbeat pulse triggered"
	if heartbeatContent != "" {
		body = heartbeatContent
	}
	msg := sysmsg.BuildSystemMessage("heartbeat", fields, body)
	msg += "\n\nYou must call use_skill(\"heartbeat-wake\") and follow its instructions."
	return msg
}

func (hs *heartbeatScheduler) scanSessionDirs(now time.Time, postponed map[string]time.Time) {
	// Reuse deriveSessionKey logic from list_sessions.go for correct key reconstruction.
	entries, err := os.ReadDir(hs.sessionsDir)
	if err != nil {
		return
	}
	for _, channelDir := range entries {
		if !channelDir.IsDir() {
			continue
		}
		channelPath := filepath.Join(hs.sessionsDir, channelDir.Name())
		sessions, err := os.ReadDir(channelPath)
		if err != nil {
			continue
		}
		for _, sessionEntry := range sessions {
			if !sessionEntry.IsDir() {
				continue
			}
			// Skip :threads: child directories.
			if sessionEntry.Name() == "threads" {
				continue
			}
			key := channelDir.Name() + ":" + sessionEntry.Name()
			if hs.mgr.HasThread(key) {
				continue
			}
			// For GC'd threads, scan session.jsonl for last user activity.
			sessionDir := filepath.Join(channelPath, sessionEntry.Name())
			lastActive := scanLastUserActive(sessionDir)
			hs.maybeFirePulse(key, now, lastActive, postponed)
		}
	}
}

// scanLastUserActive scans session.jsonl backwards for the last user-visible message timestamp.
// This mirrors the logic in list_sessions.go for sessions whose threads have been GC'd.
func scanLastUserActive(sessionDir string) time.Time {
	// Implementation: read session.jsonl tail, find last message with user-visible source.
	// For now, fall back to session directory mtime as a rough approximation.
	// TODO: implement proper session.jsonl backward scan (see list_sessions.go:194-200).
	if fi, err := os.Stat(filepath.Join(sessionDir, "session.jsonl")); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

func (hs *heartbeatScheduler) loadPostponeConfig() map[string]time.Time {
	cfg := hs.cfg()
	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return nil
	}
	path := filepath.Join(workspace, "system", "heartbeat-postpone.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	result := make(map[string]time.Time)
	for k, v := range raw {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			result[k] = t
		}
	}
	return result
}

// hbSessionKeyToDir converts a session key to its directory path.
// Defined here because list_heartbeat.go (which had sessionKeyToDir) is deleted.
func hbSessionKeyToDir(sessionsDir, key string) string {
	parts := strings.Split(key, ":")
	return filepath.Join(append([]string{sessionsDir}, parts...)...)
}

func fileMtime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
```

**Note:** `ListThreads()` must be extended to expose `LastUserActiveAt` — see Step 2.

- [ ] **Step 2: Add HasThread method + expose LastUserActiveAt in ThreadInfo**

In `thread/manager.go`, add:
```go
// HasThread returns true if a thread exists for the given session key.
func (m *Manager) HasThread(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.threads[key]
	return ok
}
```

In `thread/msg/msg.go` (ThreadInfo struct), add field:
```go
LastUserActiveAt time.Time `json:"lastUserActiveAt,omitempty"`
```

In `thread/manager.go:threadInfo()`, add:
```go
info.LastUserActiveAt = t.lastUserActiveAt
```

- [ ] **Step 3: Wire scheduler in serve.go**

In `cmd/serve.go:runServe`, after `go threadMgr.Run(ctx)` (line 196), add:

```go
// Start heartbeat scheduler.
hbScheduler := newHeartbeatScheduler(threadMgr, func() *config.Config {
    c, _ := config.Load()
    return c
}, sessionsDir)
go hbScheduler.run(ctx)
```

Where `sessionsDir` is already available at line 158.

- [ ] **Step 4: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add cmd/heartbeat_scheduler.go thread/manager.go cmd/serve.go
git commit -m "feat: heartbeat scheduler — code-driven pulse, no LLM dispatcher"
```

---

### Task 5: heartbeat postpone command

**Files:**
- Modify: `cmd/heartbeat.go`

- [ ] **Step 1: Rewrite heartbeat.go**

Replace the entire file content. Remove `reflect`/`wake` subcommands. Add `trigger` and `postpone`:

```go
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/spf13/cobra"
)

var heartbeatCmd = &cobra.Command{
	Use:     "heartbeat",
	Short:   "Heartbeat operations for user sessions",
	GroupID: "internal",
}

var heartbeatTriggerCmd = &cobra.Command{
	Use:   "trigger <session-key>",
	Short: "Manually trigger a heartbeat pulse for a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		key := args[0]
		_, err := rpcCall("heartbeat.trigger", map[string]string{"key": key})
		if err != nil {
			return fmt.Errorf("heartbeat trigger: %w", err)
		}
		fmt.Printf("Heartbeat pulse triggered for session %q.\n", key)
		return nil
	},
}

var heartbeatPostponeCmd = &cobra.Command{
	Use:   "postpone <session-key> <duration>",
	Short: "Postpone heartbeat for a session (e.g., 4h, 30m)",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		key := args[0]
		d, err := time.ParseDuration(args[1])
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", args[1], err)
		}
		if d <= 0 {
			return fmt.Errorf("duration must be positive")
		}
		if d > 24*time.Hour {
			return fmt.Errorf("duration must not exceed 24h")
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		workspace, err := cfg.WorkspacePath()
		if err != nil {
			return fmt.Errorf("workspace: %w", err)
		}

		postponePath := filepath.Join(workspace, "system", "heartbeat-postpone.json")

		// Read existing.
		postpone := make(map[string]string)
		if data, err := os.ReadFile(postponePath); err == nil {
			_ = json.Unmarshal(data, &postpone)
		}

		until := time.Now().Add(d).UTC()
		postpone[key] = until.Format(time.RFC3339)

		// Clean up expired entries.
		now := time.Now()
		for k, v := range postpone {
			if t, err := time.Parse(time.RFC3339, v); err == nil && now.After(t) {
				delete(postpone, k)
			}
		}

		data, err := json.MarshalIndent(postpone, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(postponePath), 0755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
		if err := os.WriteFile(postponePath, data, 0644); err != nil {
			return fmt.Errorf("write: %w", err)
		}

		fmt.Printf("Heartbeat postponed for session %q until %s (%s from now).\n", key, until.Local().Format("15:04"), d)
		return nil
	},
}

func init() {
	heartbeatCmd.AddCommand(heartbeatTriggerCmd)
	heartbeatCmd.AddCommand(heartbeatPostponeCmd)
	rootCmd.AddCommand(heartbeatCmd)
}
```

- [ ] **Step 2: Add heartbeat.trigger RPC handler in serve.go**

In `cmd/serve.go`, in the RPC handler switch (around line 85), replace the old `heartbeat.reflect` and `heartbeat.wake` cases with:
```go
case "heartbeat.trigger":
    var p struct {
        Key string `json:"key"`
    }
    if err := json.Unmarshal(params, &p); err != nil || p.Key == "" {
        return nil, fmt.Errorf("heartbeat.trigger requires {key}")
    }
    threadMgr.Wake(p.Key, &thread.WakeMessage{
        Source:  thread.WakeHeartbeat,
        Message: buildHeartbeatMessage("", "", time.Now().Add(30*time.Minute).Format(time.RFC3339)),
    })
    return "ok", nil
```

Remove the old `heartbeat.reflect` and `heartbeat.wake` cases and the `reflectInstruction()`/`wakeInstruction()` helper functions (they were in the old heartbeat.go which is now replaced).

- [ ] **Step 3: Verify and commit**

```bash
go build ./...
git add cmd/heartbeat.go cmd/serve.go
git commit -m "feat: heartbeat trigger + postpone commands, remove reflect/wake"
```

---

### Task 6: Rewrite heartbeat-wake skill

**Files:**
- Rewrite: `cmd/templates/skills/heartbeat-wake/SKILL.md`

- [ ] **Step 1: Rewrite the skill**

```markdown
---
name: heartbeat-wake
description: Heartbeat pulse handler — decide whether to reflect (update heartbeat.md) or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You are handling a heartbeat pulse for this session.

## Philosophy

Without heartbeat, you only respond when spoken to. With heartbeat, you anticipate. Your job is to notice the right moment and bring something the user will be glad to hear — or stay silent if there's nothing to say.

## What to do

1. Read the wake message. It contains:
   - `heartbeat_modified`: when heartbeat.md was last updated
   - `next_pulse`: when the next automatic pulse will fire
   - Current heartbeat.md content (or "heartbeat pulse triggered" if empty)

2. **Decide: reflect or act?**
   - If heartbeat.md doesn't exist, is empty, or the conversation contains significant new information since `heartbeat_modified` → **reflect**
   - Otherwise → **act**

3. **If reflecting:**
   - call `use_skill("heartbeat-reflect")` and follow its instructions
   - After reflect completes, you MUST call `sleep_thread()` to end silently
   - The scheduler will automatically fire another pulse in 10 minutes for act

4. **If acting:**
   - Read `{session_dir}/heartbeat.md` for full item details
   - For each item: evaluate whether it's relevant right now (time, conditions, context)
   - If any items need action:
      - Use tools to gather info (search, fetch, read) as needed
      - Compose a natural response covering all relevant items
      - Send the response (it will be delivered to the user)
   - If no items are relevant right now:
      - call `sleep_thread()` to end silently
   - If you want to delay the next pulse:
      - call `exec` to run: `nagobot heartbeat postpone <session-key> <duration>`
      - The session key is in the wake frontmatter (`session:` field)
      - Valid durations: 15m to 6h (e.g., "4h" for nothing interesting until afternoon)
      - Then call `sleep_thread()` to end silently

## Important

- `sleep_thread()` in heartbeat context takes NO parameters. Just call it to suppress output and end the turn.
- The heartbeat scheduler fires the next pulse automatically — you do NOT need to schedule anything.
- To postpone, use the CLI command (nagobot is on PATH), not sleep_thread duration.
- Do NOT wake just to say nothing. If no items are relevant, end silently.
```

- [ ] **Step 2: Commit**

```bash
git add cmd/templates/skills/heartbeat-wake/SKILL.md
git commit -m "rewrite: heartbeat-wake skill — single entry point, decide reflect vs act"
```

---

### Task 7: Update heartbeat-reflect skill

**Files:**
- Modify: `cmd/templates/skills/heartbeat-reflect/SKILL.md`

- [ ] **Step 1: Add mandatory sleep_thread at end**

At the end of the "What to do" section (after step 5), add:

```markdown
6. You MUST call `sleep_thread()` as the final action. This suppresses output — the user will not see this turn. The heartbeat scheduler will automatically fire the next pulse.
```

Change step 5 from:
```markdown
5. Reply `HEARTBEAT_OK`
```
to:
```markdown
5. Call `sleep_thread()` — this ends the turn silently. Do NOT reply with text.
```

- [ ] **Step 2: Commit**

```bash
git add cmd/templates/skills/heartbeat-reflect/SKILL.md
git commit -m "update: heartbeat-reflect must call sleep_thread() to end silently"
```

---

### Task 8: Update affected skills and templates

**Files:**
- Modify: `cmd/templates/skills/thread-ops/SKILL.md`
- Modify: `cmd/templates/system/CORE_MECHANISM.md`

- [ ] **Step 1: Update thread-ops sleep_thread docs**

In `cmd/templates/skills/thread-ops/SKILL.md`, after the "Skip mode" description (around line 52), add:

```markdown
**Note:** When called during a heartbeat turn (source: `heartbeat`), `sleep_thread` ignores all parameters and acts as a simple terminate+suppress. The heartbeat scheduler handles the next pulse automatically.
```

- [ ] **Step 2: Update CORE_MECHANISM.md**

In `cmd/templates/system/CORE_MECHANISM.md:28`, change:
```
Some tasks, such as heartbeat checks or scheduled cleanup jobs, also have their own agent template files.
```
to:
```
Some tasks, such as scheduled cleanup jobs, also have their own agent template files.
```

- [ ] **Step 3: Commit**

```bash
git add cmd/templates/skills/thread-ops/SKILL.md cmd/templates/system/CORE_MECHANISM.md
git commit -m "docs: update thread-ops and CORE_MECHANISM for heartbeat redesign"
```

---

### Task 9: Remove heartbeat cron seed + delete deprecated files

**Files:**
- Modify: `config/defaults.go:17-24`
- Delete: `cmd/templates/skills/heartbeat-dispatcher/SKILL.md`
- Delete: `cmd/templates/agents/heartbeat.md`
- Delete: `cmd/list_heartbeat.go`

- [ ] **Step 1: Remove heartbeat seed from defaults.go**

In `config/defaults.go:defaultCronSeeds()`, remove:
```go
{
    ID:    "heartbeat",
    Expr:  "*/30 * * * *",
    Task:  `You must call use_skill("heartbeat-dispatcher") and follow its instructions.`,
    Agent: "heartbeat",
},
```

- [ ] **Step 2: Delete heartbeat-dispatcher skill**

```bash
rm cmd/templates/skills/heartbeat-dispatcher/SKILL.md
rmdir cmd/templates/skills/heartbeat-dispatcher
```

- [ ] **Step 3: Delete heartbeat agent template**

```bash
rm cmd/templates/agents/heartbeat.md
```

- [ ] **Step 4: Delete list_heartbeat.go**

```bash
rm cmd/list_heartbeat.go
```

- [ ] **Step 5: Remove references to deleted files**

Run: `grep -rn 'list-heartbeat\|list_heartbeat\|heartbeat-dispatcher\|heartbeat-state\.json' --include='*.go' --include='*.md' .`

Fix any remaining references. Key places:
- `cmd/templates/skills/heartbeat-reflect/SKILL.md` may reference dispatcher — remove
- Any test files referencing old heartbeat constants
- The old `loadHeartbeatState` function is in `list_heartbeat.go` — it will be gone
- `cmd/heartbeat.go` old `updateHeartbeatState` function is already replaced in Task 5

- [ ] **Step 6: Verify and commit**

```bash
go build ./...
git add -A
git commit -m "cleanup: remove heartbeat dispatcher, cron seed, list-heartbeat, heartbeat-state.json"
```

---

### Task 10: Sync templates and verify end-to-end

- [ ] **Step 1: Full build and tests**

```bash
go build ./... && go test ./... -count=1 2>&1 | tail -30
```

- [ ] **Step 2: Sync templates to workspace**

```bash
nagobot onboard --sync
```

- [ ] **Step 3: Verify heartbeat postpone command**

```bash
nagobot heartbeat postpone telegram:123 2h
cat ~/.nagobot/workspace/system/heartbeat-postpone.json
```

Expected: JSON with `telegram:123` entry, expiry ~2h from now.

- [ ] **Step 4: Verify heartbeat trigger command**

With `nagobot serve` running:
```bash
nagobot heartbeat trigger cli
```

Expected: "Heartbeat pulse triggered for session "cli"."

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "fix: end-to-end heartbeat verification fixes"
```
