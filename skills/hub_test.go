package skills

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	w.Close()
	return buf.Bytes()
}

func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" {
			http.NotFound(w, r)
			return
		}
		resp := searchResponse{
			Page: []skillEntry{
				{Slug: "hello-world", DisplayName: "Hello World", Summary: "Greets the user"},
				{Slug: "git-helper", DisplayName: "Git Helper", Summary: "Git utilities"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewHubClient(srv.URL)
	results, err := client.Search("hello", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Slug != "hello-world" {
		t.Errorf("expected slug 'hello-world', got %q", results[0].Slug)
	}
	if results[0].Name != "Hello World" {
		t.Errorf("expected name 'Hello World', got %q", results[0].Name)
	}
}

func TestSearchEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(searchResponse{})
	}))
	defer srv.Close()

	client := NewHubClient(srv.URL)
	results, err := client.Search("nonexistent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewHubClient(srv.URL)
	_, err := client.Search("test", 10)
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestInstallZip(t *testing.T) {
	skillContent := "---\nname: test-skill\n---\n# Test\nDo stuff."
	zipData := makeTestZip(t, map[string]string{
		"SKILL.md": skillContent,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipData)
	}))
	defer srv.Close()

	destDir := t.TempDir()
	client := NewHubClient(srv.URL)
	if err := client.Install("test-skill", destDir); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "test-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != skillContent {
		t.Errorf("content mismatch: got %q", string(content))
	}
}

func TestInstallZipMultiFile(t *testing.T) {
	zipData := makeTestZip(t, map[string]string{
		"SKILLS.md": "---\nname: multi\n---\n# Multi",
		"helper.py": "print('hello')",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipData)
	}))
	defer srv.Close()

	destDir := t.TempDir()
	client := NewHubClient(srv.URL)
	if err := client.Install("multi", destDir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(destDir, "multi", "SKILLS.md")); err != nil {
		t.Error("SKILLS.md not found")
	}
	if _, err := os.Stat(filepath.Join(destDir, "multi", "helper.py")); err != nil {
		t.Error("helper.py not found")
	}
}

func TestInstallRawFallback(t *testing.T) {
	// Non-ZIP body should be saved as SKILL.md directly.
	body := "---\nname: raw\n---\n# Raw skill"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	destDir := t.TempDir()
	client := NewHubClient(srv.URL)
	if err := client.Install("raw-skill", destDir); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "raw-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != body {
		t.Errorf("content mismatch")
	}
}

func TestInstallNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewHubClient(srv.URL)
	err := client.Install("nonexistent", t.TempDir())
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestExtractZipPathTraversal(t *testing.T) {
	// Malicious ZIP with path traversal should be skipped.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../../../etc/passwd")
	f.Write([]byte("evil"))
	w.Close()

	destDir := t.TempDir()
	if err := extractZip(buf.Bytes(), destDir); err != nil {
		t.Fatal(err)
	}

	// The evil file should NOT exist.
	if _, err := os.Stat(filepath.Join(destDir, "..", "..", "..", "etc", "passwd")); err == nil {
		t.Error("path traversal file should not have been extracted")
	}
}

func TestInstalledSkillsTracking(t *testing.T) {
	workspace := t.TempDir()

	installed, err := LoadInstalled(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed.Skills) != 0 {
		t.Errorf("expected empty, got %d", len(installed.Skills))
	}

	installed.Track("hello-world", "https://clawhub.ai")
	if err := installed.Save(workspace); err != nil {
		t.Fatal(err)
	}

	installed2, err := LoadInstalled(workspace)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := installed2.IsTracked("hello-world")
	if !ok {
		t.Fatal("expected hello-world to be tracked")
	}
	if meta.Hub != "https://clawhub.ai" {
		t.Errorf("expected hub URL, got %q", meta.Hub)
	}

	installed2.Untrack("hello-world")
	if _, ok := installed2.IsTracked("hello-world"); ok {
		t.Error("expected untracked")
	}
}

func TestNewHubClientCustom(t *testing.T) {
	c := NewHubClient("https://custom.hub/")
	if c.BaseURL != "https://custom.hub" {
		t.Errorf("expected trailing slash stripped, got %q", c.BaseURL)
	}
}

func TestIsZip(t *testing.T) {
	if !isZip([]byte("PK\x03\x04rest")) {
		t.Error("expected true for valid ZIP magic")
	}
	if isZip([]byte("not a zip")) {
		t.Error("expected false for non-ZIP")
	}
	if isZip([]byte("PK")) {
		t.Error("expected false for too-short data")
	}
}

func TestInstallSizeLimitExceeded(t *testing.T) {
	// Serve a body larger than maxSkillSize.
	big := make([]byte, maxSkillSize+100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(big)
	}))
	defer srv.Close()

	client := NewHubClient(srv.URL)
	err := client.Install("too-big", t.TempDir())
	if err == nil {
		t.Fatal("expected error for oversized download")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("expected size limit error, got: %v", err)
	}
}
