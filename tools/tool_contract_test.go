package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the tool argument contract documented in tools.go: empty
// string means "not provided", and any field that needs to distinguish "absent"
// from "present but empty" must be a pointer. Each case below is a defect that
// shipped, so the test is the guard rail rather than a formality.

// --- write_file: a dropped `content` key must never truncate the file --------

func writeFileResult(t *testing.T, tool *WriteFileTool, argsJSON string) string {
	t.Helper()
	return tool.Run(context.Background(), json.RawMessage(argsJSON))
}

func TestWriteFile_MissingContentRejectedAndFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(path, []byte("precious"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &WriteFileTool{workspace: dir}
	args, _ := json.Marshal(map[string]any{"path": path})
	result := writeFileResult(t, tool, string(args))

	if !strings.Contains(result, "missing or empty required argument") || !strings.Contains(result, "content") {
		t.Fatalf("expected missing-content rejection, got: %s", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "precious" {
		t.Fatalf("file was modified by a rejected call: %q", got)
	}
}

func TestWriteFile_NullContentRejectedAndFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(path, []byte("precious"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &WriteFileTool{workspace: dir}
	result := writeFileResult(t, tool, `{"path":`+jsonQuote(path)+`,"content":null}`)

	if !strings.Contains(result, "missing or empty required argument") {
		t.Fatalf("expected null-content rejection, got: %s", result)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "precious" {
		t.Fatalf("file was modified by a rejected call: %q", got)
	}
}

func TestWriteFile_ExplicitEmptyContentCreatesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	tool := &WriteFileTool{workspace: dir}
	result := writeFileResult(t, tool, `{"path":`+jsonQuote(path)+`,"content":""}`)

	if IsToolError(result) {
		t.Fatalf("empty content is a legitimate write, got error: %s", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file was not created: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty file, got %q", got)
	}
}

func TestWriteFile_NormalWriteStillWorks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	tool := &WriteFileTool{workspace: dir}
	result := writeFileResult(t, tool, `{"path":`+jsonQuote(path)+`,"content":"hello"}`)

	if IsToolError(result) {
		t.Fatalf("unexpected error: %s", result)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", got)
	}
}

// jsonQuote JSON-quotes a string for inline test fixtures.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- use_skill: an empty name lists the skills, it is not an error -----------

type stubSkillProvider struct {
	names   []string
	prompts map[string]string
}

func (s *stubSkillProvider) GetSkillPrompt(name string) (string, string, bool) {
	p, ok := s.prompts[name]
	return p, "", ok
}
func (s *stubSkillProvider) SkillNames() []string { return s.names }
func (s *stubSkillProvider) Reload() error        { return nil }

func TestUseSkill_EmptyNameListsSkills(t *testing.T) {
	tool := NewUseSkillTool(&stubSkillProvider{names: []string{"research", "push"}})

	for _, args := range []string{`{"name":""}`, `{}`, `{"name":null}`} {
		result := tool.Run(context.Background(), json.RawMessage(args))
		if !strings.Contains(result, "Available skills") {
			t.Fatalf("args %s: expected the skill list, got: %s", args, result)
		}
		if !strings.Contains(result, "research") || !strings.Contains(result, "push") {
			t.Fatalf("args %s: skill names missing from list: %s", args, result)
		}
	}
}

func TestUseSkill_NamedSkillStillLoads(t *testing.T) {
	tool := NewUseSkillTool(&stubSkillProvider{
		names:   []string{"research"},
		prompts: map[string]string{"research": "RESEARCH PROMPT BODY"},
	})
	result := tool.Run(context.Background(), json.RawMessage(`{"name":"research"}`))
	if !strings.Contains(result, "RESEARCH PROMPT BODY") {
		t.Fatalf("expected the skill prompt, got: %s", result)
	}
}

// --- exec: a whitespace-only confirm is "no token", not a bad token ----------

func TestExec_WhitespaceConfirmTreatedAsAbsent(t *testing.T) {
	tool := newTestExecTool()
	result := runExec(t, tool, "rm -rf /tmp/nagobot-test-xyz", "   ")

	// The old behaviour compared " " against the HMAC and returned
	// "invalid confirmation token", which a model that cannot omit fields could
	// never escape: every retry sent whitespace again.
	if strings.Contains(result, "invalid confirmation token") {
		t.Fatalf("whitespace confirm must read as absent, got the invalid-token loop: %s", result)
	}
	if !strings.Contains(result, "Dangerous command detected") {
		t.Fatalf("expected the confirmation prompt with a fresh token, got: %s", result)
	}
}
