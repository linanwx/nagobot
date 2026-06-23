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

	// Copy sections directory.
	sectionsSrc := filepath.Join(srcDir, "system", "sections")
	sectionsDst := filepath.Join(ws, "system", "sections")
	if err := os.MkdirAll(sectionsDst, 0755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(sectionsSrc)
	if err != nil {
		t.Fatalf("read sections dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sectionsSrc, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(sectionsDst, e.Name()), data, 0644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}

	// Copy agents directory.
	agentsSrc := filepath.Join(srcDir, "agents")
	agentsDst := filepath.Join(ws, "agents")
	if err := os.MkdirAll(agentsDst, 0755); err != nil {
		t.Fatal(err)
	}
	agentEntries, err := os.ReadDir(agentsSrc)
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}
	for _, e := range agentEntries {
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

	// Create shared SectionRegistry.
	secReg := NewSectionRegistry(filepath.Join(ws, "system", "sections"))
	if err := secReg.Load(); err != nil {
		t.Fatalf("load sections: %v", err)
	}

	for _, name := range agentNames(t, ws) {
		t.Run(name, func(t *testing.T) {
			a, err := reg.New(name)
			if err != nil {
				t.Fatalf("New(%q): %v", name, err)
			}

			a.SetSections(secReg)
			a.SetLocation(now.Location())
			a.Set("TOOLS", "tool_a, tool_b, tool_c")
			a.Set("SKILLS", "skill_x: does X\nskill_y: does Y")
			a.Set("TASK", "Test task content")
			a.Set(SectionUserMemory, "## User Preferences\n\nTest user preferences")
			a.Set(SectionHeartbeatPrompt, "## Heartbeat\n\nTest heartbeat section")

			prompt := a.Build()

			if prompt == "" {
				t.Fatal("Build() returned empty prompt")
			}

			// Check no unresolved {{...}} placeholders remain.
			if idx := strings.Index(prompt, "{{"); idx >= 0 {
				end := strings.Index(prompt[idx:], "}}")
				placeholder := prompt[idx:]
				if end >= 0 {
					placeholder = prompt[idx : idx+end+2]
				}
				start := max(idx-40, 0)
				stop := min(idx+60, len(prompt))
				t.Errorf("unresolved placeholder %s\ncontext: ...%s...", placeholder, prompt[start:stop])
			}

			if !strings.Contains(prompt, "tool_a, tool_b, tool_c") {
				t.Error("{{TOOLS}} was not resolved")
			}
			if !strings.Contains(prompt, "skill_x: does X") {
				t.Error("{{SKILLS}} was not resolved")
			}
			if !strings.Contains(prompt, now.Format("2006-01-02")) {
				t.Error("{{DATE}} was not resolved")
			}
			if !strings.Contains(prompt, "Available agents") {
				t.Error("{{AGENTS}} was not resolved")
			}

			t.Logf("prompt length: %d chars", len(prompt))
		})
	}
}

// TestPeopleKnowledge_UserMemoryAgents verifies that system/people_knowledge.md
// is injected into every agent that grounds on the user (declares
// user_memory_section) — soul and the search sub-agent — but NOT into agents
// without user memory (the world-knowledge cron runner), and that nothing is
// injected when the file is absent.
func TestPeopleKnowledge_UserMemoryAgents(t *testing.T) {
	ws := setupWorkspace(t)
	reg := NewRegistry(ws)

	build := func(name string) string {
		a, err := reg.New(name)
		if err != nil {
			t.Fatalf("New(%s): %v", name, err)
		}
		a.SetLocation(time.Now().Location())
		return a.Build()
	}

	const marker = "PEOPLE_KNOWLEDGE_MARKER_q2x9"

	// Absent file: no empty people_knowledge block, even for a user-memory agent.
	if strings.Contains(build("soul"), "type: people_knowledge") {
		t.Fatalf("people_knowledge block injected while file absent")
	}

	// Write the file. It must appear for agents that declare user_memory_section.
	pkPath := filepath.Join(ws, "system", "people_knowledge.md")
	if err := os.WriteFile(pkPath, []byte("# People Knowledge\n\n"+marker), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"soul", "search"} {
		if out := build(name); !strings.Contains(out, marker) || !strings.Contains(out, "type: people_knowledge") {
			t.Errorf("%s prompt missing people_knowledge block (%q)", name, marker)
		}
	}
	// An agent without user_memory_section (cron runner) must stay clean.
	if out := build("world-knowledge"); strings.Contains(out, marker) {
		t.Errorf("people_knowledge leaked into world-knowledge agent prompt")
	}
}

// TestSearchAgent_HasMemorySections verifies the search sub-agent opts into
// user_memory_section + memory_index_section, so a delegated search turn carries
// the user's memory and memory index in its system prompt (same as pre-think).
// Guards against the sections being dropped from search.md frontmatter.
func TestSearchAgent_HasMemorySections(t *testing.T) {
	ws := setupWorkspace(t)
	reg := NewRegistry(ws)

	secReg := NewSectionRegistry(filepath.Join(ws, "system", "sections"))
	if err := secReg.Load(); err != nil {
		t.Fatalf("load sections: %v", err)
	}

	a, err := reg.New("search")
	if err != nil {
		t.Fatalf("New(search): %v", err)
	}
	a.SetSections(secReg)
	a.SetLocation(time.Now().Location())
	a.Set("TOOLS", "tool_a")
	a.Set("SKILLS", "skill_x: does X")

	const userMark = "USER_MEMORY_MARKER_7f3a"
	const memMark = "MEMORY_INDEX_MARKER_b91c"
	a.Set(SectionUserMemory, userMark)
	a.Set(SectionMemoryIndex, memMark)

	prompt := a.Build()
	if !strings.Contains(prompt, userMark) {
		t.Errorf("search agent prompt missing user_memory_section content (%q)", userMark)
	}
	if !strings.Contains(prompt, memMark) {
		t.Errorf("search agent prompt missing memory_index_section content (%q)", memMark)
	}
}
