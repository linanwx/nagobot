package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuiltinSkillTemplatesAllLoad loads the skills the binary ships with,
// exactly as the daemon does, and fails on any that does not parse.
//
// The daemon tolerates a broken skill on purpose (TestOneBadSkillDoesNotKillTheRest):
// it logs one Warn and serves the rest. That is right for third-party skills,
// and it is why a broken BUILT-IN skill goes unnoticed. dispatch-guide shipped
// in v1.7.95 with an unquoted description containing "`event: turn_ended`",
// which YAML reads as a mapping, and every deployment ran without that skill
// for ten days while the only symptom was a startup Warn. A built-in skill is
// ours, so failing to load it must fail the build.
func TestBuiltinSkillTemplatesAllLoad(t *testing.T) {
	dir := filepath.Join("..", "cmd", "templates", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	loaded, err := loadSkillsFromDirectory(dir)
	if err != nil {
		t.Fatalf("built-in skills failed to load: %v", err)
	}

	for _, e := range entries {
		if !e.IsDir() || FindSkillFile(filepath.Join(dir, e.Name())) == "" {
			continue
		}
		s, ok := loaded[e.Name()]
		if !ok {
			t.Errorf("built-in skill %s did not load", e.Name())
			continue
		}
		if s.Description == "" {
			t.Errorf("built-in skill %s has no description; the description is what routes it", e.Name())
		}
	}
}
