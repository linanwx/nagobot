package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOneBadSkillDoesNotKillTheRest pins the blast radius of a malformed
// SKILL.md. A skill directory is not curated — `manage-skills` installs from
// ClawHub, where thousands of community skills are one `git clone` away — so a
// single bad frontmatter must degrade to "that one skill is missing", never to
// "the assistant has no skills at all".
//
// The trap is narrower than it looks: an unquoted YAML scalar cannot contain
// ": ", so a description as ordinary as "the WRITE path: add a key" is a parse
// error. That is not hypothetical — it was hit while editing these very files.
func TestOneBadSkillDoesNotKillTheRest(t *testing.T) {
	dir := t.TempDir()
	write := func(slug, frontmatter string) {
		sub := filepath.Join(dir, slug)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\n" + frontmatter + "\n---\n\nbody of " + slug + "\n"
		if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("alpha", "description: first good skill")
	// A colon-space in an unquoted scalar. Reads fine to a human, fatal to YAML.
	write("poison", "description: the WRITE path: add a key to the config")
	write("zeta", "description: last good skill")

	r := NewRegistry()
	err := r.LoadFromDirectory(dir)

	// The error must be reported — a skill silently vanishing is worse than a
	// loud one. But it must not be load-bearing for the others.
	if err == nil {
		t.Error("bad skill parsed without error; the failure is now silent")
	} else if !strings.Contains(err.Error(), "poison") {
		t.Errorf("error does not name the offending skill: %v", err)
	}

	for _, slug := range []string{"alpha", "zeta"} {
		if _, ok := r.Get(slug); !ok {
			t.Errorf("%q was taken down by an unrelated bad skill", slug)
		}
	}
	if _, ok := r.Get("poison"); ok {
		t.Error("the malformed skill was registered anyway")
	}
}

func TestResolvePromptSubstitutesEveryPlaceholder(t *testing.T) {
	got := ResolvePrompt("{{WORKSPACE}}|{{SESSIONDIR}}|{{SESSIONKEY}}|{{SKILLDIR}}|{{WORKSPACE}}", PromptVars{
		Workspace: "/w", SessionDir: "/w/sessions/web/a", SessionKey: "web:a", SkillDir: "/w/skills/x",
	})
	if want := "/w|/w/sessions/web/a|web:a|/w/skills/x|/w"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// An empty value leaves its placeholder for the model to see, not "".
	if got := ResolvePrompt("{{SESSIONKEY}}", PromptVars{SessionKey: "  "}); got != "{{SESSIONKEY}}" {
		t.Fatalf("blank value substituted: %q", got)
	}
}
