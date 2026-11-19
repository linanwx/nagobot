package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/linanwx/nagobot/prompts"
)

// TestEmbeddedPromptFilesMatchDeclarations is the drift guard for package
// prompts. This package links every package that declares a prompt, so here
// — and only here — Declared() is the complete set the daemon validates at
// start. A prompt declared without a file, a file nothing declares, or a file
// whose placeholders disagree with its declaration would otherwise surface as
// a daemon that refuses to start after `onboard --sync`.
func TestEmbeddedPromptFilesMatchDeclarations(t *testing.T) {
	if err := prompts.Check(prompts.Embedded()); err != nil {
		t.Fatal(err)
	}
}

// TestOnboardSyncWritesALoadablePromptSet runs the sync's copy into a fresh
// directory and loads it the way the daemon does.
func TestOnboardSyncWritesALoadablePromptSet(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "prompts")
	if err := os.MkdirAll(filepath.Join(dest, "stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stale", "gone.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cleanAndCopyFS(prompts.Embedded(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "stale", "gone.md")); !os.IsNotExist(err) {
		t.Fatal("sync left a file the binary no longer ships")
	}
	if err := prompts.UseDir(dest); err != nil {
		t.Fatal(err)
	}
}
