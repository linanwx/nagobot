package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillsShResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" {
			http.NotFound(w, r)
			return
		}
		resp := skillsShSearchResponse{
			Skills: []skillsShEntry{
				{
					ID:       "microsoft/playwright-cli/playwright-cli",
					SkillID:  "playwright-cli",
					Name:     "playwright-cli",
					Installs: 9636,
					Source:   "microsoft/playwright-cli",
				},
				{
					ID:       "other/playwright-skill/playwright-cli",
					SkillID:  "playwright-cli",
					Name:     "playwright-cli",
					Installs: 75,
					Source:   "other/playwright-skill",
				},
			},
			Count: 2,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &SkillsShClient{BaseURL: srv.URL, client: srv.Client()}

	// Test resolve by source (owner/repo).
	entry, err := client.Resolve("microsoft/playwright-cli")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Source != "microsoft/playwright-cli" {
		t.Errorf("expected source microsoft/playwright-cli, got %q", entry.Source)
	}
	if entry.SkillID != "playwright-cli" {
		t.Errorf("expected skillId playwright-cli, got %q", entry.SkillID)
	}

	// Test resolve by full ID.
	entry, err = client.Resolve("microsoft/playwright-cli/playwright-cli")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "microsoft/playwright-cli/playwright-cli" {
		t.Errorf("expected full ID match, got %q", entry.ID)
	}

	// Test resolve by skillId — picks most popular.
	entry, err = client.Resolve("playwright-cli")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Source != "microsoft/playwright-cli" {
		t.Errorf("expected most popular match, got source %q", entry.Source)
	}
}

func TestSkillsShResolveNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := skillsShSearchResponse{Skills: []skillsShEntry{}, Count: 0}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &SkillsShClient{BaseURL: srv.URL, client: srv.Client()}

	_, err := client.Resolve("nonexistent/skill")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestSkillsShResolveCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := skillsShSearchResponse{
			Skills: []skillsShEntry{
				{
					ID:       "Microsoft/Playwright-CLI/playwright-cli",
					SkillID:  "playwright-cli",
					Name:     "playwright-cli",
					Installs: 100,
					Source:   "Microsoft/Playwright-CLI",
				},
			},
			Count: 1,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &SkillsShClient{BaseURL: srv.URL, client: srv.Client()}

	// Should match case-insensitively by source.
	entry, err := client.Resolve("microsoft/playwright-cli")
	if err != nil {
		t.Fatal(err)
	}
	if entry.SkillID != "playwright-cli" {
		t.Errorf("expected playwright-cli, got %q", entry.SkillID)
	}
}

func TestSkillsShDownloadFile(t *testing.T) {
	content := "test file content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file.md" {
			w.Write([]byte(content))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := &SkillsShClient{BaseURL: srv.URL, client: srv.Client()}

	body, err := client.downloadFile(srv.URL + "/file.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != content {
		t.Errorf("got %q, want %q", string(body), content)
	}

	// Test 404.
	_, err = client.downloadFile(srv.URL + "/missing.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSkillsShInstallEndToEnd(t *testing.T) {
	skillContent := "---\nname: my-skill\ndescription: Test skill\n---\n# My Skill\nTest content."
	refContent := "# Reference\nGuide content."

	var srvURL string
	mux := http.NewServeMux()

	// skills.sh search API
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		resp := skillsShSearchResponse{
			Skills: []skillsShEntry{
				{
					ID:       "testorg/testrepo/my-skill",
					SkillID:  "my-skill",
					Name:     "my-skill",
					Installs: 42,
					Source:   "testorg/testrepo",
				},
			},
			Count: 1,
		}
		json.NewEncoder(w).Encode(resp)
	})

	// GitHub raw content (SKILL.md)
	mux.HandleFunc("/raw/testorg/testrepo/main/skills/my-skill/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(skillContent))
	})

	// GitHub Contents API (references/)
	mux.HandleFunc("/api/repos/testorg/testrepo/contents/skills/my-skill/references", func(w http.ResponseWriter, r *http.Request) {
		entries := []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"download_url"`
			Type        string `json:"type"`
		}{
			{Name: "guide.md", DownloadURL: srvURL + "/raw/guide.md", Type: "file"},
		}
		json.NewEncoder(w).Encode(entries)
	})

	// Reference file download
	mux.HandleFunc("/raw/guide.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(refContent))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	// Create a client that points raw and API URLs to our test server.
	// We override the Install method behavior by creating a custom client
	// and calling internal methods directly.
	client := &SkillsShClient{BaseURL: srv.URL, client: srv.Client()}

	// Test Resolve.
	entry, err := client.Resolve("testorg/testrepo")
	if err != nil {
		t.Fatal(err)
	}
	if entry.SkillID != "my-skill" {
		t.Errorf("expected my-skill, got %q", entry.SkillID)
	}

	// Test downloadFile for SKILL.md.
	body, err := client.downloadFile(srv.URL + "/raw/testorg/testrepo/main/skills/my-skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != skillContent {
		t.Errorf("SKILL.md content mismatch: got %q", string(body))
	}

	// Test downloadDirRecursive (single-level: references/ has one file).
	tmpDir := t.TempDir()
	if err := client.downloadDirRecursive(srv.URL+"/api/repos/testorg/testrepo/contents/skills/my-skill/references", filepath.Join(tmpDir, "references")); err != nil {
		t.Fatal(err)
	}

	guide, err := os.ReadFile(filepath.Join(tmpDir, "references", "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(guide) != refContent {
		t.Errorf("guide.md: got %q, want %q", string(guide), refContent)
	}
}

func TestSkillsShDownloadDirRecursive(t *testing.T) {
	mux := http.NewServeMux()
	var srvURL string

	// Root listing: README.md (file) + scripts/ (dir).
	mux.HandleFunc("/api/repos/o/r/contents/skills/s", func(w http.ResponseWriter, r *http.Request) {
		entries := []map[string]string{
			{"name": "SKILL.md", "type": "file", "download_url": srvURL + "/raw/SKILL.md"},
			{"name": "scripts", "type": "dir", "url": srvURL + "/api/repos/o/r/contents/skills/s/scripts"},
		}
		json.NewEncoder(w).Encode(entries)
	})

	// scripts/ listing: nested dir office/ + a top-level helper.py.
	mux.HandleFunc("/api/repos/o/r/contents/skills/s/scripts", func(w http.ResponseWriter, r *http.Request) {
		entries := []map[string]string{
			{"name": "helper.py", "type": "file", "download_url": srvURL + "/raw/helper.py"},
			{"name": "office", "type": "dir", "url": srvURL + "/api/repos/o/r/contents/skills/s/scripts/office"},
		}
		json.NewEncoder(w).Encode(entries)
	})

	// scripts/office/ listing: unpack.py.
	mux.HandleFunc("/api/repos/o/r/contents/skills/s/scripts/office", func(w http.ResponseWriter, r *http.Request) {
		entries := []map[string]string{
			{"name": "unpack.py", "type": "file", "download_url": srvURL + "/raw/unpack.py"},
		}
		json.NewEncoder(w).Encode(entries)
	})

	// Raw files.
	mux.HandleFunc("/raw/SKILL.md", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("skill")) })
	mux.HandleFunc("/raw/helper.py", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("helper")) })
	mux.HandleFunc("/raw/unpack.py", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("unpack")) })

	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	client := &SkillsShClient{BaseURL: srv.URL, client: srv.Client()}
	dest := t.TempDir()
	if err := client.downloadDirRecursive(srv.URL+"/api/repos/o/r/contents/skills/s", dest); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		"SKILL.md":                  "skill",
		"scripts/helper.py":         "helper",
		"scripts/office/unpack.py":  "unpack",
	} {
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s: got %q want %q", path, got, want)
		}
	}
}
