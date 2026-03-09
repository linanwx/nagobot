package skills

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	hubHTTPTimeout = 30 * time.Second
	maxSkillSize   = 10 << 20 // 10 MB download limit
)

// HubClient interacts with a ClawHub-compatible skill hub.
type HubClient struct {
	BaseURL string
	client  *http.Client
}

// NewHubClient creates a client for the given hub URL.
func NewHubClient(baseURL string) *HubClient {
	return &HubClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: hubHTTPTimeout},
	}
}

// --- API response types ---

// searchResponse handles both paginated (Page) and flat (Results) hub API shapes.
type searchResponse struct {
	Page    []skillEntry `json:"page"`
	Results []skillEntry `json:"results"`
}

func (r *searchResponse) entries() []skillEntry {
	if len(r.Page) > 0 {
		return r.Page
	}
	return r.Results
}

type skillEntry struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	OwnerName   string `json:"ownerName"`
	Owner       string `json:"owner"`
}

func (e *skillEntry) effectiveName() string {
	if e.DisplayName != "" {
		return e.DisplayName
	}
	if e.Name != "" {
		return e.Name
	}
	return e.Slug
}

func (e *skillEntry) effectiveDescription() string {
	if e.Summary != "" {
		return e.Summary
	}
	return e.Description
}

func (e *skillEntry) effectiveOwner() string {
	if e.OwnerName != "" {
		return e.OwnerName
	}
	return e.Owner
}

// --- Public types ---

// SearchResult is a skill found via hub search.
type SearchResult struct {
	Slug        string
	Name        string
	Description string
	Owner       string
}

// Search searches the hub for skills matching the query.
func (c *HubClient) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	endpoint := fmt.Sprintf("%s/api/v1/search?q=%s&limit=%d",
		c.BaseURL, url.QueryEscape(query), limit)

	resp, err := c.client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("cannot reach hub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub returned %s", resp.Status)
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}

	var results []SearchResult
	for _, e := range sr.entries() {
		results = append(results, SearchResult{
			Slug:        e.Slug,
			Name:        e.effectiveName(),
			Description: e.effectiveDescription(),
			Owner:       e.effectiveOwner(),
		})
	}
	return results, nil
}

// Install downloads a skill by slug and extracts it to skillsDir/{slug}/.
// Downloads are limited to maxSkillSize. Content is extracted to a temp dir
// first, then atomically renamed into place.
func (c *HubClient) Install(slug string, skillsDir string) error {
	endpoint := fmt.Sprintf("%s/api/v1/download?slug=%s",
		c.BaseURL, url.QueryEscape(slug))

	resp, err := c.client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillSize+1))
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}
	if len(body) > maxSkillSize {
		return fmt.Errorf("download exceeds %d MB limit", maxSkillSize>>20)
	}

	// Extract to temp dir, then rename atomically.
	tmpDir, err := os.MkdirTemp(skillsDir, ".install-*")
	if err != nil {
		return fmt.Errorf("cannot create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) // cleanup on any error path

	if isZip(body) {
		if err := extractZip(body, tmpDir); err != nil {
			return fmt.Errorf("extract failed: %w", err)
		}
	} else {
		if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), body, 0644); err != nil {
			return err
		}
	}

	// Atomic swap: remove old, rename temp into place.
	destDir := filepath.Join(skillsDir, slug)
	os.RemoveAll(destDir)
	return os.Rename(tmpDir, destDir)
}

// isZip checks the ZIP magic number (PK\x03\x04).
func isZip(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4
}

func extractZip(data []byte, destDir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		// Prevent path traversal.
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			continue
		}

		destPath := filepath.Join(destDir, cleanName)
		if !strings.HasPrefix(filepath.Clean(destPath), filepath.Clean(destDir)) {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return err
		}

		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}

	return nil
}

// --- Installed skills tracking ---

// InstalledSkills tracks which skills were installed from a hub.
type InstalledSkills struct {
	Skills map[string]*InstalledMeta `json:"skills"`
}

// InstalledMeta records the origin of an installed skill.
type InstalledMeta struct {
	Hub         string    `json:"hub"`
	InstalledAt time.Time `json:"installedAt"`
}

const installedDir = ".skillhub"
const installedFile = "installed.json"

// LoadInstalled loads the installed skills tracking file from workspace.
func LoadInstalled(workspace string) (*InstalledSkills, error) {
	path := filepath.Join(workspace, installedDir, installedFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &InstalledSkills{Skills: make(map[string]*InstalledMeta)}, nil
		}
		return nil, err
	}

	var installed InstalledSkills
	if err := json.Unmarshal(data, &installed); err != nil {
		return nil, err
	}
	if installed.Skills == nil {
		installed.Skills = make(map[string]*InstalledMeta)
	}
	return &installed, nil
}

// Save persists the installed skills tracking file.
func (is *InstalledSkills) Save(workspace string) error {
	dir := filepath.Join(workspace, installedDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(is, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, installedFile), data, 0644)
}

// Track marks a skill as installed from a hub.
func (is *InstalledSkills) Track(skillName, hubURL string) {
	is.Skills[skillName] = &InstalledMeta{
		Hub:         hubURL,
		InstalledAt: time.Now(),
	}
}

// Untrack removes tracking for a skill.
func (is *InstalledSkills) Untrack(skillName string) {
	delete(is.Skills, skillName)
}

// IsTracked checks whether a skill is tracked as hub-installed.
func (is *InstalledSkills) IsTracked(skillName string) (*InstalledMeta, bool) {
	m, ok := is.Skills[skillName]
	return m, ok
}
