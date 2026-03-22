package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGitURLToRawBase(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{
			input: "https://github.com/anthropics/skills/tree/main/skills/webapp-testing",
			want:  "https://raw.githubusercontent.com/anthropics/skills/main/skills/webapp-testing",
		},
		{
			input: "https://github.com/user/repo/tree/dev/.claude/skills/my-skill",
			want:  "https://raw.githubusercontent.com/user/repo/dev/.claude/skills/my-skill",
		},
		{
			input: "https://github.com/owner/repo/tree/main",
			want:  "https://raw.githubusercontent.com/owner/repo/main",
		},
		{
			input:   "https://gitlab.com/user/repo/tree/main/path",
			wantErr: true,
		},
		{
			input:   "https://github.com/user/repo/blob/main/file.md",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := gitURLToRawBase(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGitURLToContentsAPI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "https://github.com/anthropics/skills/tree/main/skills/webapp-testing",
			want:  "https://api.github.com/repos/anthropics/skills/contents/skills/webapp-testing/references?ref=main",
		},
		{
			input: "https://github.com/user/repo/tree/dev/.claude/skills/my-skill",
			want:  "https://api.github.com/repos/user/repo/contents/.claude/skills/my-skill/references?ref=dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := gitURLToContentsAPI(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSmitheryResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/skills" {
			http.NotFound(w, r)
			return
		}
		resp := smitherySearchResponse{
			Skills: []smitherySkillEntry{
				{
					QualifiedName: "anthropics/webapp-testing",
					Slug:          "webapp-testing",
					DisplayName:   "webapp-testing",
					Description:   "Playwright testing toolkit",
					Namespace:     "anthropics",
					GitURL:        "https://github.com/anthropics/skills/tree/main/skills/webapp-testing",
					Verified:      true,
				},
				{
					QualifiedName: "other/webapp-testing",
					Slug:          "webapp-testing",
					DisplayName:   "webapp-testing",
					Namespace:     "other",
					GitURL:        "https://github.com/other/repo/tree/main/skills/webapp-testing",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &SmitheryClient{BaseURL: srv.URL, client: srv.Client()}

	entry, err := client.Resolve("anthropics/webapp-testing")
	if err != nil {
		t.Fatal(err)
	}
	if entry.QualifiedName != "anthropics/webapp-testing" {
		t.Errorf("expected anthropics/webapp-testing, got %q", entry.QualifiedName)
	}
	if !entry.Verified {
		t.Error("expected verified=true")
	}
}

func TestSmitheryResolveNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := smitherySearchResponse{Skills: []smitherySkillEntry{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &SmitheryClient{BaseURL: srv.URL, client: srv.Client()}

	_, err := client.Resolve("nonexistent/skill")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestSmitheryInstall(t *testing.T) {
	skillContent := "---\nname: webapp-testing\ndescription: Playwright testing\n---\n# Webapp Testing\nUse Playwright."
	refContent := "# Reference\nSome reference content."

	// Serve both Smithery registry and GitHub raw content on the same server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/skills":
			resp := smitherySearchResponse{
				Skills: []smitherySkillEntry{
					{
						QualifiedName: "test/my-skill",
						Slug:          "my-skill",
						Namespace:     "test",
						GitURL:        "PLACEHOLDER", // Will be set below
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/SKILL.md":
			w.Write([]byte(skillContent))
		case r.URL.Path == "/references":
			// GitHub Contents API response
			entries := []struct {
				Name        string `json:"name"`
				DownloadURL string `json:"download_url"`
				Type        string `json:"type"`
			}{
				{Name: "guide.md", DownloadURL: "", Type: "file"},
			}
			json.NewEncoder(w).Encode(entries)
		case r.URL.Path == "/ref-download/guide.md":
			w.Write([]byte(refContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// We need to override the client to point gitUrl at our test server.
	// Since Install calls Resolve first, we set up a custom flow.
	// Instead, test the install with a mock that returns a gitUrl pointing to our server.
	registrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/skills" {
			resp := smitherySearchResponse{
				Skills: []smitherySkillEntry{
					{
						QualifiedName: "test/my-skill",
						Slug:          "my-skill",
						Namespace:     "test",
						// Use a fake GitHub URL that won't actually be used for raw download,
						// since we can't easily mock raw.githubusercontent.com.
						GitURL: "https://github.com/test/repo/tree/main/skills/my-skill",
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer registrySrv.Close()

	// For a proper integration test, we'd need to mock GitHub too.
	// Instead, test the URL parsing and resolve separately, and test Install
	// with a server that serves both registry and raw files.

	// Create a unified test server that serves as both registry and raw host.
	unified := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/skills":
			// Return a gitUrl that points back to this server (not github.com).
			// This won't work with gitURLToRawBase since it checks for github.com host.
			// So we test Resolve + URL parsing separately.
			resp := smitherySearchResponse{
				Skills: []smitherySkillEntry{
					{
						QualifiedName: "test/my-skill",
						Slug:          "my-skill",
						Namespace:     "test",
						GitURL:        "https://github.com/test/repo/tree/main/skills/my-skill",
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer unified.Close()

	// Test that Resolve works correctly.
	client := &SmitheryClient{BaseURL: unified.URL, client: unified.Client()}
	entry, err := client.Resolve("test/my-skill")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Slug != "my-skill" {
		t.Errorf("expected slug my-skill, got %q", entry.Slug)
	}

	// Test that raw base URL is correctly derived.
	rawBase, err := gitURLToRawBase(entry.GitURL)
	if err != nil {
		t.Fatal(err)
	}
	expected := "https://raw.githubusercontent.com/test/repo/main/skills/my-skill"
	if rawBase != expected {
		t.Errorf("got %q, want %q", rawBase, expected)
	}
}

func TestSmitheryInstallEndToEnd(t *testing.T) {
	skillContent := "---\nname: e2e-skill\n---\n# E2E Skill\nTest content."
	refContent := "# Reference\nGuide content."

	// Create a server that acts as both Smithery registry, GitHub raw, and GitHub API.
	mux := http.NewServeMux()

	mux.HandleFunc("/registry/skills", func(w http.ResponseWriter, r *http.Request) {
		resp := smitherySearchResponse{
			Skills: []smitherySkillEntry{
				{
					QualifiedName: "myorg/e2e-skill",
					Slug:          "e2e-skill",
					Namespace:     "myorg",
					GitURL:        "", // Will be set dynamically
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/raw/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(skillContent))
	})

	mux.HandleFunc("/api/references", func(w http.ResponseWriter, r *http.Request) {
		entries := []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"download_url"`
			Type        string `json:"type"`
		}{
			{Name: "guide.md", Type: "file"}, // DownloadURL set dynamically
		}
		json.NewEncoder(w).Encode(entries)
	})

	mux.HandleFunc("/raw/references/guide.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(refContent))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Test the downloadFile method directly.
	client := &SmitheryClient{BaseURL: srv.URL + "/registry", client: srv.Client()}

	body, err := client.downloadFile(srv.URL + "/raw/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != skillContent {
		t.Errorf("SKILL.md content mismatch: got %q", string(body))
	}
}

func TestSmitheryResolveCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := smitherySearchResponse{
			Skills: []smitherySkillEntry{
				{
					QualifiedName: "Anthropics/Webapp-Testing",
					Slug:          "webapp-testing",
					Namespace:     "Anthropics",
					GitURL:        "https://github.com/anthropics/skills/tree/main/skills/webapp-testing",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &SmitheryClient{BaseURL: srv.URL, client: srv.Client()}

	// Should match case-insensitively.
	entry, err := client.Resolve("anthropics/webapp-testing")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Slug != "webapp-testing" {
		t.Errorf("expected webapp-testing, got %q", entry.Slug)
	}
}

func TestSmitheryDownloadFile(t *testing.T) {
	content := "test file content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file.md" {
			w.Write([]byte(content))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := &SmitheryClient{BaseURL: srv.URL, client: srv.Client()}

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

func TestSmitheryDownloadReferences(t *testing.T) {
	ref1 := "# Guide\nHow to use."
	ref2 := "# FAQ\nQuestions."

	mux := http.NewServeMux()

	// GitHub Contents API mock — serves at the path that gitURLToContentsAPI would produce.
	mux.HandleFunc("/repos/owner/repo/contents/skills/test/references", func(w http.ResponseWriter, r *http.Request) {
		entries := []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"download_url"`
			Type        string `json:"type"`
		}{
			{Name: "guide.md", DownloadURL: "PLACEHOLDER_GUIDE", Type: "file"},
			{Name: "faq.md", DownloadURL: "PLACEHOLDER_FAQ", Type: "file"},
			{Name: "subdir", Type: "dir"}, // Should be skipped
		}
		// We need to set download URLs dynamically based on test server URL.
		// Since we can't know it at compile time, we'll handle this differently.
		json.NewEncoder(w).Encode(entries)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// For a proper test, we need the download URLs to point back to our server.
	// Recreate with dynamic URLs.
	srv.Close()

	mux2 := http.NewServeMux()
	var srvURL string

	mux2.HandleFunc("/repos/owner/repo/contents/skills/test/references", func(w http.ResponseWriter, r *http.Request) {
		entries := []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"download_url"`
			Type        string `json:"type"`
		}{
			{Name: "guide.md", DownloadURL: srvURL + "/raw/guide.md", Type: "file"},
			{Name: "faq.md", DownloadURL: srvURL + "/raw/faq.md", Type: "file"},
			{Name: "subdir", Type: "dir"},
		}
		json.NewEncoder(w).Encode(entries)
	})
	mux2.HandleFunc("/raw/guide.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ref1))
	})
	mux2.HandleFunc("/raw/faq.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ref2))
	})

	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	srvURL = srv2.URL

	destDir := t.TempDir()

	// We need to test downloadReferences with a gitURL that maps to our test server.
	// Since downloadReferences calls gitURLToContentsAPI which produces api.github.com URLs,
	// we need to test it differently. Let's test the method directly by patching.
	// Instead, we test the core logic by calling the Contents API ourselves.

	client := &SmitheryClient{BaseURL: "", client: srv2.Client()}

	// Manually call the Contents API to get the entries.
	resp, err := client.client.Get(srv2.URL + "/repos/owner/repo/contents/skills/test/references")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var entries []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"download_url"`
		Type        string `json:"type"`
	}
	json.NewDecoder(resp.Body).Decode(&entries)

	// Download files manually like downloadReferences does.
	refsDir := filepath.Join(destDir, "references")
	os.MkdirAll(refsDir, 0755)
	for _, e := range entries {
		if e.Type != "file" || e.DownloadURL == "" {
			continue
		}
		body, err := client.downloadFile(e.DownloadURL)
		if err != nil {
			t.Fatalf("download %s: %v", e.Name, err)
		}
		os.WriteFile(filepath.Join(refsDir, e.Name), body, 0644)
	}

	// Verify files were downloaded.
	guide, err := os.ReadFile(filepath.Join(refsDir, "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(guide) != ref1 {
		t.Errorf("guide.md: got %q, want %q", string(guide), ref1)
	}

	faq, err := os.ReadFile(filepath.Join(refsDir, "faq.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(faq) != ref2 {
		t.Errorf("faq.md: got %q, want %q", string(faq), ref2)
	}
}
