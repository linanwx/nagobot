# Discord DM channel.json Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix Discord DM heartbeat wake delivery failure (404 "Unknown Channel") by persisting DM routing info in `channel.json` and using it in the default sink.

**Architecture:** Discord DM session keys are `discord:{userID}`. The per-message sink uses the DM channel ID from message metadata and works fine. But the default sink (used by heartbeat wake) extracts the user ID from the session key and passes it as a raw channel ID — Discord returns 404. Fix: when a Discord DM message arrives, write `{sessionDir}/channel.json` with `discord_dm.reply_to = "dm:{userID}"`. The default sink checks for this file first and uses the `dm:` prefixed reply_to, which triggers `UserChannelCreate` in `discord.Send()`.

**Tech Stack:** Go

---

## File Map

| File | Change | Responsibility |
|------|--------|---------------|
| `cmd/dispatcher.go` | Add write | Write `channel.json` on Discord DM message arrival |
| `cmd/serve.go:257-272` | Add read | Default sink reads `channel.json` before falling back to session key parsing |

---

### Task 1: Write channel.json on Discord DM message arrival

**Files:**
- Modify: `cmd/dispatcher.go`

- [ ] **Step 1: Add helper function to write channel.json**

Add at the end of `cmd/dispatcher.go`:

```go
// persistChannelRouting writes channel.json to the session directory for
// channels that need routing metadata beyond what the session key provides
// (e.g., Discord DM needs "dm:{userID}" to create a DM channel on send).
func persistChannelRouting(sessionsDir, sessionKey string, msg *channel.Message) {
	if msg == nil {
		return
	}
	chatType := strings.TrimSpace(msg.Metadata["chat_type"])
	if chatType != "dm" {
		return
	}

	channelName := ""
	if strings.HasPrefix(msg.ChannelID, "discord:") {
		channelName = "discord"
	}
	if channelName == "" {
		return // only Discord DM for now
	}

	userID := strings.TrimSpace(msg.UserID)
	if userID == "" {
		return
	}

	// Build session directory path from session key.
	parts := strings.Split(sessionKey, ":")
	sessionDir := filepath.Join(append([]string{sessionsDir}, parts...)...)

	data := map[string]any{
		"discord_dm": map[string]string{
			"channel":  "discord",
			"reply_to": "dm:" + userID,
			"user_id":  userID,
		},
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(sessionDir, 0755)
	_ = os.WriteFile(filepath.Join(sessionDir, "channel.json"), raw, 0644)
}
```

- [ ] **Step 2: Call persistChannelRouting in the dispatch path**

In the `Dispatch` method (around line 80-90), after `sessionKey := d.route(msg)` and before `d.threads.Wake(...)`, add:

```go
	if sessionsDir, err := d.cfg.SessionsDir(); err == nil {
		persistChannelRouting(sessionsDir, sessionKey, msg)
	}
```

Note: `d.cfg` is the dispatcher's config. Check if `SessionsDir()` is available — it may need to be accessed via `config.Load()` or passed in. Read the Dispatcher struct to determine how config is accessed.

- [ ] **Step 3: Add necessary imports**

Add `"encoding/json"`, `"os"`, `"path/filepath"` to imports if not already present.

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add cmd/dispatcher.go
git commit -m "feat: persist channel.json for Discord DM routing metadata"
```

---

### Task 2: Default sink reads channel.json

**Files:**
- Modify: `cmd/serve.go:257-272`

- [ ] **Step 1: Add helper to read channel.json**

Add before `buildDefaultSinkFor`:

```go
// readChannelRouting reads {sessionDir}/channel.json and returns the discord_dm
// reply_to value if present. Returns empty string if not found or not applicable.
func readDiscordDMReplyTo(sessionsDir, sessionKey string) string {
	parts := strings.SplitN(sessionKey, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	sessionDir := filepath.Join(sessionsDir, parts[0], parts[1])
	data, err := os.ReadFile(filepath.Join(sessionDir, "channel.json"))
	if err != nil {
		return ""
	}
	var routing struct {
		DiscordDM *struct {
			ReplyTo string `json:"reply_to"`
		} `json:"discord_dm"`
	}
	if err := json.Unmarshal(data, &routing); err != nil || routing.DiscordDM == nil {
		return ""
	}
	return routing.DiscordDM.ReplyTo
}
```

- [ ] **Step 2: Update Discord section in buildDefaultSinkFor**

Replace the discord block (lines 257-272):

```go
		// discord:{channelOrUserID} → check channel.json for DM routing, fallback to raw ID.
		if strings.HasPrefix(sessionKey, "discord:") {
			channelID := strings.TrimPrefix(sessionKey, "discord:")
			if channelID != "" {
				replyTo := channelID
				if r := readDiscordDMReplyTo(sessionsDir, sessionKey); r != "" {
					replyTo = r
				}
				return thread.Sink{
					Label:      "your response will be sent to discord channel " + channelID,
					Chunkable: true,
					Send: func(ctx context.Context, response string) error {
						if strings.TrimSpace(response) == "" {
							return nil
						}
						return chMgr.SendTo(ctx, "discord", response, replyTo)
					},
				}
			}
		}
```

- [ ] **Step 3: Pass sessionsDir to the closure**

The `buildDefaultSinkFor` function needs access to `sessionsDir`. Add it as a parameter or derive it from `cfg`:

Change the signature from:
```go
func buildDefaultSinkFor(chMgr *channel.Manager, cfg *config.Config) func(string) thread.Sink {
```
to:
```go
func buildDefaultSinkFor(chMgr *channel.Manager, cfg *config.Config, sessionsDir string) func(string) thread.Sink {
```

Update the caller in `cmd/serve.go` (around line 158) to pass `sessionsDir`:
```go
threadMgr.SetDefaultSinkFor(buildDefaultSinkFor(chManager, cfg, sessionsDir))
```

Check that `sessionsDir` is available at the call site. If not, derive it via `cfg.SessionsDir()`.

- [ ] **Step 4: Add necessary imports**

Add `"encoding/json"`, `"os"`, `"path/filepath"` to imports in `serve.go` if not already present.

- [ ] **Step 5: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add cmd/serve.go
git commit -m "fix: Discord DM default sink reads channel.json for correct routing

When heartbeat wake triggers for a Discord DM session, the default sink
now reads channel.json to get the dm:{userID} reply_to address. This
triggers UserChannelCreate in discord.Send() instead of using the raw
user ID as a channel ID (which returns 404)."
```

---

### Task 3: Verify end-to-end

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 2: Run all tests**

Run: `go test ./... -count=1 2>&1 | tail -20`
Expected: all pass.

- [ ] **Step 3: Manual verification**

Send a Discord DM to the bot, then check if `channel.json` was created in the session directory:
```bash
cat ~/.nagobot/workspace/sessions/discord/<userID>/channel.json
```
Expected: `{"discord_dm": {"channel": "discord", "reply_to": "dm:<userID>", "user_id": "<userID>"}}`
