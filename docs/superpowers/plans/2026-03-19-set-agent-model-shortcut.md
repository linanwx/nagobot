# Set-Agent Model Shortcut Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One-command session model switching via `set-agent --session X --provider P --model M`, implicit specialty→model routing, `cli -m` one-shot mode, and removal of `agent` command.

**Architecture:** `set-agent` gains `--provider/--model` flags. When used, it auto-generates a `fixed-to-<slug>.md` agent template with `specialty: <model>` and optional `provider: <provider>` in frontmatter. `TemplateMeta` gains a `Provider` field. `resolvedModelConfig()` adds an implicit fallback: if specialty matches a registered model name, auto-route to that model (using frontmatter `provider` or registry lookup). `cli` gains `-m` flag for one-shot messages. `agent` command is removed.

**Tech Stack:** Go

---

## File Map

| File | Change | Responsibility |
|------|--------|---------------|
| `agent/template_meta.go:10-15` | Add field | `TemplateMeta.Provider` |
| `agent/registry.go:15-20` | Add field | `AgentDef.Provider` |
| `provider/provider.go` | Add func | `ProviderForModel(modelType) string` — reverse lookup |
| `thread/run.go:343-364` | Add fallback | Implicit specialty→model routing in `resolvedModelConfig()` |
| `cmd/set_agent.go` | Add flags + logic | `--provider/--model` → auto-create agent + assign session |
| `cmd/cli_client.go` | Add flag | `-m` one-shot message mode |
| `cmd/agent.go` | Delete | Remove deprecated command |
| `cmd/templates/skills/session-ops/SKILL.md:190-205` | Rewrite | Update Per-Session Model Switching section |

---

### Task 1: Add `Provider` field to agent TemplateMeta and AgentDef

**Files:**
- Modify: `agent/template_meta.go:10-15`
- Modify: `agent/registry.go:15-20`

- [ ] **Step 1: Add Provider to TemplateMeta**

In `agent/template_meta.go`, add to `TemplateMeta` struct after `Specialty`:

```go
Provider    string `yaml:"provider"`
```

- [ ] **Step 2: Add Provider to AgentDef**

In `agent/registry.go`, add to `AgentDef` struct after `Specialty`:

```go
Provider    string // Provider name declared in frontmatter (optional, used for model-pinned agents)
```

- [ ] **Step 3: Wire Provider in registry loading**

Find where `AgentDef` is populated from `TemplateMeta` in `agent/registry.go` and add `Provider: meta.Provider`.

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add agent/template_meta.go agent/registry.go
git commit -m "feat: add Provider field to agent TemplateMeta and AgentDef"
```

---

### Task 2: Add `ProviderForModel()` reverse lookup

**Files:**
- Modify: `provider/provider.go`

- [ ] **Step 1: Add the function**

Add after `ContextWindowForModel()`:

```go
// ProviderForModel returns the first provider that supports the given model type.
// Returns empty string if no provider is found.
func ProviderForModel(modelType string) string {
	for provName, models := range providerModelTypes {
		for _, m := range models {
			if m == modelType {
				return provName
			}
		}
	}
	return ""
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add provider/provider.go
git commit -m "feat: add ProviderForModel reverse lookup"
```

---

### Task 3: Implicit specialty→model routing in resolvedModelConfig

**Files:**
- Modify: `thread/run.go:343-364`

- [ ] **Step 1: Add implicit fallback after explicit routing table miss**

In `thread/run.go`, replace `resolvedModelConfig()` (lines 343-364):

```go
func (t *Thread) resolvedModelConfig() *config.ModelConfig {
	cfg := t.cfg()
	if t.Agent == nil || cfg.Agents == nil {
		return nil
	}
	def := cfg.Agents.Def(t.Agent.Name)
	if def == nil || def.Specialty == "" {
		return nil
	}
	models := cfg.Models
	if cfg.ModelsFn != nil {
		models = cfg.ModelsFn()
	}
	// Explicit routing table lookup.
	if len(models) > 0 {
		if mc, ok := models[def.Specialty]; ok && mc != nil {
			return mc
		}
	}
	// Implicit: if specialty matches a registered model name, auto-route.
	if provider.IsSupportedModel(def.Specialty) {
		prov := def.Provider // from agent frontmatter
		if prov == "" {
			prov = provider.ProviderForModel(def.Specialty)
		}
		if prov != "" {
			return &config.ModelConfig{
				Provider:  prov,
				ModelType: def.Specialty,
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: Add `IsSupportedModel` to provider package**

In `provider/provider.go`, add:

```go
// IsSupportedModel returns true if the model type is registered in any provider.
func IsSupportedModel(modelType string) bool {
	return supportedModelTypes[modelType]
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Run tests**

Run: `go test ./thread/... -count=1`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add thread/run.go provider/provider.go
git commit -m "feat: implicit specialty→model routing when specialty matches registered model"
```

---

### Task 4: Extend set-agent with --provider/--model flags

**Files:**
- Modify: `cmd/set_agent.go`

- [ ] **Step 1: Add flags and auto-create logic**

Replace the entire `cmd/set_agent.go` with:

```go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/provider"
	"github.com/spf13/cobra"
)

var setAgentCmd = &cobra.Command{
	Use:     "set-agent",
	Short:   "Set or clear the agent for a session",
	GroupID: "internal",
	Long: `Set the agent assigned to a session key in config.yaml.

The running server detects config changes automatically, so the new agent
takes effect on the next message in that session.

Examples:
  nagobot set-agent --session "discord:123456" --agent fallout
  nagobot set-agent --session "discord:123456" --provider openrouter --model xiaomi/mimo-v2-pro
  nagobot set-agent --session "discord:123456"                  # clear override`,
	RunE: runSetAgent,
}

var (
	setAgentSession  string
	setAgentName     string
	setAgentProvider string
	setAgentModel    string
)

func init() {
	setAgentCmd.Flags().StringVar(&setAgentSession, "session", "", "Session key (required)")
	setAgentCmd.Flags().StringVar(&setAgentName, "agent", "", "Agent name (empty to clear)")
	setAgentCmd.Flags().StringVar(&setAgentProvider, "provider", "", "Provider for model-pinned agent (used with --model)")
	setAgentCmd.Flags().StringVar(&setAgentModel, "model", "", "Model type — auto-creates a fixed agent (used with --provider)")
	_ = setAgentCmd.MarkFlagRequired("session")
	rootCmd.AddCommand(setAgentCmd)
}

func runSetAgent(_ *cobra.Command, _ []string) error {
	session := strings.TrimSpace(setAgentSession)
	if session == "" {
		return fmt.Errorf("--session is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	modelArg := strings.TrimSpace(setAgentModel)
	providerArg := strings.TrimSpace(setAgentProvider)
	agentArg := strings.TrimSpace(setAgentName)

	// --provider/--model mode: auto-create agent.
	if modelArg != "" {
		if providerArg == "" {
			providerArg = provider.ProviderForModel(modelArg)
			if providerArg == "" {
				return fmt.Errorf("unknown model %q and no --provider specified", modelArg)
			}
		}
		if err := provider.ValidateProviderModelType(providerArg, modelArg); err != nil {
			return fmt.Errorf("invalid provider/model: %w", err)
		}
		agentName, agentPath, err := createFixedAgent(cfg, providerArg, modelArg)
		if err != nil {
			return err
		}
		agentArg = agentName

		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: %s\nagent_path: %s\nspecialty: %s\nprovider: %s\nmodel: %s\n---\n\n",
			session, agentName, agentPath, modelArg, providerArg, modelArg)
		fmt.Printf("Created agent %q at %s\n", agentName, agentPath)
		fmt.Printf("Specialty %q → %s / %s (implicit routing)\n", modelArg, providerArg, modelArg)
	}

	if cfg.Channels == nil {
		cfg.Channels = &config.ChannelsConfig{}
	}
	if cfg.Channels.SessionAgents == nil {
		cfg.Channels.SessionAgents = make(map[string]string)
	}

	if agentArg == "" && modelArg == "" {
		delete(cfg.Channels.SessionAgents, session)
	} else {
		cfg.Channels.SessionAgents[session] = agentArg
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if agentArg == "" && modelArg == "" {
		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: cleared\n---\n\nCleared agent for session %q.\n", session, session)
	} else if modelArg == "" {
		fmt.Printf("---\ncommand: set-agent\nstatus: ok\nsession: %s\nagent: %s\n---\n\nSet agent %q for session %q.\n", session, agentArg, agentArg, session)
		printAgentModelRouting(cfg, agentArg)
	} else {
		fmt.Printf("Set session %q → agent %q\n", session, agentArg)
	}
	return nil
}

func createFixedAgent(cfg *config.Config, provName, modelType string) (name, path string, err error) {
	slug := strings.ReplaceAll(modelType, "/", "-")
	name = "fixed-to-" + slug

	workspace, err := cfg.WorkspacePath()
	if err != nil {
		return "", "", fmt.Errorf("failed to get workspace: %w", err)
	}
	path = filepath.Join(workspace, "agents", name+".md")

	content := fmt.Sprintf(`---
name: %s
specialty: %s
provider: %s
---
You are a member of the nagobot family. You are a helpful assistant.

{{CORE_MECHANISM}}

{{USER}}
`, name, modelType, provName)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", "", fmt.Errorf("failed to create agents dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write agent template: %w", err)
	}
	return name, path, nil
}

func printAgentModelRouting(cfg *config.Config, agentName string) {
	for _, slot := range scanAgentModelSlots() {
		if !strings.EqualFold(slot.AgentName, agentName) {
			continue
		}
		prov, model := cfg.GetProvider(), cfg.GetModelType()
		if mc, ok := cfg.Thread.Models[slot.ModelType]; ok && mc != nil {
			prov, model = mc.Provider, mc.ModelType
		}
		fmt.Printf("Specialty: %s -> %s / %s\n", slot.ModelType, prov, model)
		return
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Test the new command**

Run: `nagobot set-agent --session test:123 --provider openrouter --model xiaomi/mimo-v2-pro`
Expected output should include:
- agent name: `fixed-to-xiaomi-mimo-v2-pro`
- agent path
- specialty → provider/model mapping

- [ ] **Step 4: Commit**

```bash
git add cmd/set_agent.go
git commit -m "feat: set-agent --provider/--model auto-creates model-pinned agent"
```

---

### Task 5: Add `-m` flag to `nagobot cli`

**Files:**
- Modify: `cmd/cli_client.go`

- [ ] **Step 1: Add the flag and one-shot logic**

Add flag variable and registration:

```go
var cliMessageFlag string

func init() {
	rootCmd.AddCommand(cliClientCmd)
	cliClientCmd.Flags().StringVarP(&cliMessageFlag, "message", "m", "", "Send a single message and exit (one-shot mode)")
}
```

In `runCLIClient`, after the connection is established (after line 46), add before the goroutines:

```go
	// One-shot mode: send message, wait for response, exit.
	if cliMessageFlag != "" {
		encoder := json.NewEncoder(conn)
		if err := encoder.Encode(socketInbound{Type: "message", Text: cliMessageFlag}); err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}
		decoder := json.NewDecoder(conn)
		var lastContent string
		for {
			var msg channel.SocketOutbound
			if err := decoder.Decode(&msg); err != nil {
				break
			}
			switch msg.Type {
			case "content":
				if len(msg.Text) > len(lastContent) {
					fmt.Print(msg.Text[len(lastContent):])
				}
				if msg.Final {
					fmt.Println()
					return nil
				}
				lastContent = msg.Text
			case "error":
				return fmt.Errorf("%s", msg.Text)
			}
		}
		return nil
	}
```

Remove the `fmt.Println("Connected to nagobot daemon...")` line to before the goroutines but after the one-shot check.

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Test one-shot mode**

Run: `nagobot cli -m "hello"`
Expected: prints response and exits.

- [ ] **Step 4: Commit**

```bash
git add cmd/cli_client.go
git commit -m "feat: add -m flag to cli for one-shot message mode"
```

---

### Task 6: Remove `agent` command

**Files:**
- Delete: `cmd/agent.go`

- [ ] **Step 1: Delete the file**

```bash
rm cmd/agent.go
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success (no other file references `agentCmd` or `runAgent`).

- [ ] **Step 3: Commit**

```bash
git add cmd/agent.go
git commit -m "remove: delete deprecated agent command, replaced by cli -m"
```

---

### Task 7: Update session-ops skill

**Files:**
- Modify: `cmd/templates/skills/session-ops/SKILL.md:156-205`

- [ ] **Step 1: Update set-agent section and Per-Session Model Switching**

Replace the `set-agent` section (lines 156-205) with:

````markdown
## set-agent

Set or clear the agent for a session.

```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key> --agent <agent_name>
```

Set a specific provider/model for a session (auto-creates a fixed agent):
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key> --provider <provider> --model <model>
```

Clear the agent override (revert to default):
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key>
```

- `--session`: session key (required). Examples: `discord:123456`, `telegram:78910`, `cli`.
- `--agent`: agent template name from `agents/*.md`. Omit or empty to clear the override.
- `--provider`: provider name. Used with `--model` to auto-create a model-pinned agent.
- `--model`: model type. Used with `--provider`. Auto-creates `agents/fixed-to-<model-slug>.md` with implicit specialty routing.

Output includes: agent name, agent file path, specialty name, and specialty→model mapping.

## Per-Session Model Switching

When a user asks to use a specific model for a session:

**Case 1: User wants to switch to an existing agent** — use `set-agent --session <key> --agent <name>`.

**Case 2: User wants a specific provider/model** — use `set-agent --session <key> --provider <provider> --model <model>`. This auto-creates a fixed agent and sets up implicit routing in one step. No manual agent creation or routing config needed.
````

- [ ] **Step 2: Commit**

```bash
git add cmd/templates/skills/session-ops/SKILL.md
git commit -m "docs: update session-ops skill for set-agent --provider/--model shortcut"
```

---

### Task 8: Build, test, verify

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 2: Run all tests**

Run: `go test ./... -count=1 2>&1 | tail -20`
Expected: all pass.

- [ ] **Step 3: End-to-end test**

Run:
```bash
nagobot set-agent --session test:e2e --provider openrouter --model xiaomi/mimo-v2-pro
nagobot cli -m "say hello"
nagobot set-agent --session test:e2e  # clean up
```
