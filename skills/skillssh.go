package skills

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	skillsShBaseURL = "https://skills.sh"
)

// SkillsShClient interacts with the skills.sh skill registry.
type SkillsShClient struct {
	BaseURL string
	client  *http.Client
}

// NewSkillsShClient creates a client for the skills.sh registry.
func NewSkillsShClient() *SkillsShClient {
	return &SkillsShClient{
		BaseURL: skillsShBaseURL,
		client:  &http.Client{Timeout: hubHTTPTimeout},
	}
}

// --- skills.sh API response types ---

type skillsShSearchResponse struct {
	Skills []skillsShEntry `json:"skills"`
	Count  int             `json:"count"`
}

type skillsShEntry struct {
	ID       string `json:"id"`       // e.g. "microsoft/playwright-cli/playwright-cli"
	SkillID  string `json:"skillId"`  // e.g. "playwright-cli"
	Name     string `json:"name"`     // e.g. "playwright-cli"
	Installs int    `json:"installs"` // install count
	Source   string `json:"source"`   // e.g. "microsoft/playwright-cli" (GitHub owner/repo)
}

// Resolve finds a skill by slug on skills.sh.
// The slug can be:
//   - "owner/repo" — picks the first skill from that repo
//   - "owner/repo/skillId" — picks that exact skill
//   - "skillId" — searches by name, picks the most popular match
func (c *SkillsShClient) Resolve(slug string) (*skillsShEntry, error) {
	endpoint := fmt.Sprintf("%s/api/search?q=%s&limit=50",
		c.BaseURL, url.QueryEscape(slug))

	resp, err := c.client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("cannot reach skills.sh registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills.sh returned %s", resp.Status)
	}

	var sr skillsShSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("invalid skills.sh response: %w", err)
	}

	if len(sr.Skills) == 0 {
		return nil, fmt.Errorf("skill %q not found on skills.sh", slug)
	}

	// Try exact match by full ID (owner/repo/skillId).
	for i := range sr.Skills {
		if strings.EqualFold(sr.Skills[i].ID, slug) {
			return &sr.Skills[i], nil
		}
	}

	// Try match by source (owner/repo) — pick the first (most popular).
	for i := range sr.Skills {
		if strings.EqualFold(sr.Skills[i].Source, slug) {
			return &sr.Skills[i], nil
		}
	}

	// Try match by skillId — pick the first (most popular).
	for i := range sr.Skills {
		if strings.EqualFold(sr.Skills[i].SkillID, slug) {
			return &sr.Skills[i], nil
		}
	}

	// Fall back to first result.
	return &sr.Skills[0], nil
}

// Install downloads a skill from skills.sh and saves it to skillsDir/{skillName}/.
// The slug is resolved via the skills.sh API, then files are downloaded from GitHub.
func (c *SkillsShClient) Install(slug string, skillsDir string) (skillName string, err error) {
	entry, err := c.Resolve(slug)
	if err != nil {
		return "", err
	}

	if entry.Source == "" {
		return "", fmt.Errorf("skill %q has no source repository", slug)
	}

	// Construct GitHub raw base URL from source (owner/repo) and skillId.
	// Skills live at: github.com/{source}/tree/main/skills/{skillId}
	rawBase := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/skills/%s",
		entry.Source, entry.SkillID)

	// Construct GitHub Contents API URL for references.
	contentsRefsURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/skills/%s/references?ref=main",
		entry.Source, entry.SkillID)

	// Create temp dir for atomic install.
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create skills dir: %w", err)
	}
	tmpDir, err := os.MkdirTemp(skillsDir, ".install-*")
	if err != nil {
		return "", fmt.Errorf("cannot create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Download SKILL.md (required).
	skillMDURL := rawBase + "/SKILL.md"
	body, err := c.downloadFile(skillMDURL)
	if err != nil {
		return "", fmt.Errorf("cannot download SKILL.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), body, 0644); err != nil {
		return "", err
	}

	// Try to download references/ directory via GitHub Contents API.
	// This is best-effort — many skills don't have references.
	c.downloadRefsFromAPI(contentsRefsURL, tmpDir)

	skillName = entry.SkillID

	// Atomic swap.
	destDir := filepath.Join(skillsDir, skillName)
	os.RemoveAll(destDir)
	if err := os.Rename(tmpDir, destDir); err != nil {
		return "", err
	}

	return skillName, nil
}

// downloadFile fetches a URL and returns the body, with size limits.
func (c *SkillsShClient) downloadFile(rawURL string) ([]byte, error) {
	resp, err := c.client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s for %s", resp.Status, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSkillSize {
		return nil, fmt.Errorf("file exceeds %d MB limit", maxSkillSize>>20)
	}

	return body, nil
}

// downloadRefsFromAPI downloads files from a GitHub Contents API URL into destDir/references/.
func (c *SkillsShClient) downloadRefsFromAPI(contentsURL string, destDir string) {
	resp, err := c.client.Get(contentsURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	defer resp.Body.Close()

	var entries []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"download_url"`
		Type        string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return
	}

	if len(entries) == 0 {
		return
	}

	refsDir := filepath.Join(destDir, "references")
	if err := os.MkdirAll(refsDir, 0755); err != nil {
		return
	}

	for _, e := range entries {
		if e.Type != "file" || e.DownloadURL == "" {
			continue
		}
		body, err := c.downloadFile(e.DownloadURL)
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(refsDir, e.Name), body, 0644)
	}
}
