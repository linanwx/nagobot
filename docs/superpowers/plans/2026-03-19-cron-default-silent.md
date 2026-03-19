# Cron Remove Silent Flag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove `--silent` flag from cron CLI. Silent behavior is achieved by leaving `--wake-session` empty. Simplifies the mental model: no wake_session = no delivery.

**Architecture:** Remove `--silent` flag and all `silent` metadata references. Dispatcher checks only `reportTo == ""` for silent. Built-in cron jobs already have empty `WakeSession` so they naturally become silent. Keep `Silent` field in Job struct for backward-compatible deserialization of persisted jobs, but stop writing it.

**Tech Stack:** Go, Markdown

---

### Task 1: Dispatcher — silent = empty wake_session only

**Files:**
- Modify: `cmd/dispatcher.go`

- [ ] **Step 1: Simplify buildCronSink**

In `buildCronSink`, replace:

```go
	silent := msg.Metadata["silent"] == "true"
	reportTo := strings.TrimSpace(msg.Metadata["wake_session"])
	if reportTo == "" {
		reportTo = "cli"
	}
	jobID := strings.TrimSpace(msg.Metadata["job_id"])

	if silent {
		return thread.Sink{Label: "cron silent, result will not be delivered"}
	}
```

with:

```go
	reportTo := strings.TrimSpace(msg.Metadata["wake_session"])
	jobID := strings.TrimSpace(msg.Metadata["job_id"])

	if reportTo == "" {
		return thread.Sink{Label: "cron silent, result will not be delivered"}
	}
```

- [ ] **Step 2: Verify and commit**

```bash
go build ./... && go test ./cmd/... -count=1
git add cmd/dispatcher.go
git commit -m "fix: cron silent determined by empty wake_session only"
```

---

### Task 2: Remove --silent flag from CLI

**Files:**
- Modify: `cmd/cron.go`

- [ ] **Step 1: Remove silent flag variable and registration**

Find and remove `commonSilent` variable declaration and `cmd.Flags().BoolVar(&commonSilent, "silent", ...)` registration in `addCommonJobFlags()`.

- [ ] **Step 2: Remove silent from applyCommonJobFlags**

In `applyCommonJobFlags()`, remove `job.Silent = commonSilent`.

- [ ] **Step 3: Update --wake-session help text**

Change the `--wake-session` flag help from current text to: `"Session to receive execution result (omit for silent — no delivery)"`

- [ ] **Step 4: Verify and commit**

```bash
go build ./... && go test ./cmd/... -count=1
git add cmd/cron.go
git commit -m "remove: --silent flag from cron CLI, use empty wake-session instead"
```

---

### Task 3: Remove silent from cron channel metadata

**Files:**
- Modify: `channel/cron.go`

- [ ] **Step 1: Remove silent from buildMessage metadata**

In `buildMessage()`, find where `metadata["silent"]` is set and remove that line. Keep `metadata["wake_session"]`.

- [ ] **Step 2: Remove silent from buildCronStartMessage**

In `buildCronStartMessage()`, find where `"silent"` is included in the system message fields map and remove it.

- [ ] **Step 3: Verify and commit**

```bash
go build ./...
git add channel/cron.go
git commit -m "cleanup: remove silent metadata from cron channel messages"
```

---

### Task 4: Remove Silent from built-in cron job defaults

**Files:**
- Modify: `config/defaults.go`

- [ ] **Step 1: Remove Silent: true from default jobs**

In `defaultCronSeeds()`, remove `Silent: true` from heartbeat, tidyup, and session-summary jobs. They already have empty `WakeSession`, so they'll be silent by default.

- [ ] **Step 2: Verify and commit**

```bash
go build ./...
git add config/defaults.go
git commit -m "cleanup: remove Silent field from built-in cron jobs (empty WakeSession is sufficient)"
```

---

### Task 5: Update manage-cron skill

**Files:**
- Modify: `cmd/templates/skills/manage-cron/SKILL.md`

- [ ] **Step 1: Remove all --silent references**

Remove `--silent` from all command examples and flag documentation. Update guidance: to make a job silent, simply omit `--wake-session`. If `--wake-session` is provided, results are delivered to that session.

- [ ] **Step 2: Commit**

```bash
git add cmd/templates/skills/manage-cron/SKILL.md
git commit -m "docs: update manage-cron skill — remove --silent, use empty wake-session"
```

---

### Task 6: Verify end-to-end

- [ ] **Step 1: Full build and tests**

Run: `go build ./... && go test ./... -count=1 2>&1 | tail -20`

- [ ] **Step 2: Verify existing cron jobs still work**

Run: `nagobot cron list` — built-in jobs should show without `silent` field, and have empty `wake_session`.
