package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupWorkspace copies embedded templates into a temp workspace directory
// and returns the workspace path.
func setupWorkspace(t *testing.T) string {
	t.Helper()

	srcDir := filepath.Join("..", "cmd", "templates")
	ws := t.TempDir()

	// Copy top-level files: CORE_MECHANISM.md, USER.md
	for _, name := range []string{"CORE_MECHANISM.md", "USER.md"} {
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(ws, name), data, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Copy agents directory
	agentsSrc := filepath.Join(srcDir, "agents")
	agentsDst := filepath.Join(ws, "agents")
	if err := os.MkdirAll(agentsDst, 0755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(agentsSrc)
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(agentsSrc, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(agentsDst, e.Name()), data, 0644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}

	return ws
}

// agentNames returns the list of agent names discovered in the workspace.
func agentNames(t *testing.T, ws string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(ws, "agents"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(ws, "agents", e.Name()))
		meta, _, _, _ := ParseTemplate(string(raw))
		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		names = append(names, name)
	}
	return names
}

func TestAllAgentsBuild_NoUnresolvedPlaceholders(t *testing.T) {
	ws := setupWorkspace(t)
	reg := NewRegistry(ws)
	now := time.Now()

	for _, name := range agentNames(t, ws) {
		t.Run(name, func(t *testing.T) {
			a, err := reg.New(name)
			if err != nil {
				t.Fatalf("New(%q): %v", name, err)
			}

			// Set all runtime vars that thread/run.go would set.
			a.Set("TIME", now)
			a.Set("TOOLS", "tool_a, tool_b, tool_c")
			a.Set("SKILLS", "skill_x: does X\nskill_y: does Y")
			a.Set("TASK", "Test task content")

			prompt := a.Build()

			if prompt == "" {
				t.Fatal("Build() returned empty prompt")
			}

			// Check no unresolved {{...}} placeholders remain.
			if idx := strings.Index(prompt, "{{"); idx >= 0 {
				// Extract the placeholder name for a clear error message.
				end := strings.Index(prompt[idx:], "}}")
				placeholder := prompt[idx:]
				if end >= 0 {
					placeholder = prompt[idx : idx+end+2]
				}
				// Show surrounding context.
				start := max(idx-40, 0)
				stop := min(idx+60, len(prompt))
				t.Errorf("unresolved placeholder %s\ncontext: ...%s...", placeholder, prompt[start:stop])
			}

			// Verify key content from CORE_MECHANISM was injected.
			if !strings.Contains(prompt, "tool_a, tool_b, tool_c") {
				t.Error("{{TOOLS}} was not resolved in prompt")
			}
			if !strings.Contains(prompt, "skill_x: does X") {
				t.Error("{{SKILLS}} was not resolved in prompt")
			}
			if !strings.Contains(prompt, now.Format("2006-01-02")) {
				t.Error("{{TIME}} was not resolved in prompt")
			}
			if !strings.Contains(prompt, "Available agents") {
				t.Error("{{AGENTS}} was not resolved in prompt")
			}

			t.Logf("prompt length: %d chars", len(prompt))
		})
	}
}
