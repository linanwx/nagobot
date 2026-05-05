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
	"time"
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
// Uses a longer per-request timeout than hubHTTPTimeout because tree-walk
// installs make many GitHub raw + Contents API calls in a row, and a single
// timeout on a transient slow connection would abort the whole install.
func NewSkillsShClient() *SkillsShClient {
	return &SkillsShClient{
		BaseURL: skillsShBaseURL,
		client:  &http.Client{Timeout: 60 * time.Second},
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
	sr, err := c.search(slug)
	if err != nil {
		return nil, err
	}

	// If full slug (e.g. "microsoft/playwright-cli") returns no results,
	// retry with the last segment (e.g. "playwright-cli") because skills.sh
	// fuzzy search doesn't handle "owner/repo" format well.
	if len(sr.Skills) == 0 && strings.Contains(slug, "/") {
		parts := strings.Split(slug, "/")
		shortName := parts[len(parts)-1]
		sr, err = c.search(shortName)
		if err != nil {
			return nil, err
		}
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

// search queries the skills.sh search API.
func (c *SkillsShClient) search(query string) (*skillsShSearchResponse, error) {
	endpoint := fmt.Sprintf("%s/api/search?q=%s&limit=50",
		c.BaseURL, url.QueryEscape(query))

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
	return &sr, nil
}

// Install downloads a skill from skills.sh and saves it to skillsDir/{skillName}/.
// The slug is resolved via the skills.sh API, then the entire skill directory tree
// (SKILL.md + scripts/ + references/ + any other subdirs/files) is fetched from
// GitHub via the Contents API, preserving directory structure.
func (c *SkillsShClient) Install(slug string, skillsDir string) (skillName string, err error) {
	entry, err := c.Resolve(slug)
	if err != nil {
		return "", err
	}

	if entry.Source == "" {
		return "", fmt.Errorf("skill %q has no source repository", slug)
	}

	// Root of the skill in the source repo: github.com/{source}/tree/main/skills/{skillId}
	rootContentsURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/skills/%s?ref=main",
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

	// Recursively download the entire skill directory tree.
	if err := c.downloadDirRecursive(rootContentsURL, tmpDir); err != nil {
		return "", fmt.Errorf("cannot download skill tree: %w", err)
	}

	// SKILL.md is required — fail loudly if missing rather than installing a broken skill.
	if _, err := os.Stat(filepath.Join(tmpDir, "SKILL.md")); err != nil {
		return "", fmt.Errorf("SKILL.md not found in downloaded skill")
	}

	skillName = entry.SkillID

	// Atomic swap.
	destDir := filepath.Join(skillsDir, skillName)
	os.RemoveAll(destDir)
	if err := os.Rename(tmpDir, destDir); err != nil {
		return "", err
	}

	return skillName, nil
}

// downloadDirRecursive walks a GitHub Contents API endpoint and writes every
// file to destDir, recursing into subdirectories so the output preserves the
// remote tree layout. Errors on individual files (download failure, oversized)
// abort the whole install — partial skills are worse than no skill.
func (c *SkillsShClient) downloadDirRecursive(contentsURL string, destDir string) error {
	body, err := c.downloadFile(contentsURL)
	if err != nil {
		return fmt.Errorf("list %s: %w", contentsURL, err)
	}

	var entries []struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		Type        string `json:"type"` // "file" or "dir"
		DownloadURL string `json:"download_url"`
		URL         string `json:"url"` // contents API URL for the entry (used for dir recursion)
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return fmt.Errorf("parse %s: %w", contentsURL, err)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	for _, e := range entries {
		switch e.Type {
		case "file":
			if e.DownloadURL == "" {
				continue // git-lfs or symlink — skip
			}
			body, err := c.downloadFile(e.DownloadURL)
			if err != nil {
				return fmt.Errorf("download %s: %w", e.Name, err)
			}
			if err := os.WriteFile(filepath.Join(destDir, e.Name), body, 0644); err != nil {
				return err
			}
		case "dir":
			subURL := e.URL
			if subURL == "" {
				continue
			}
			if err := c.downloadDirRecursive(subURL, filepath.Join(destDir, e.Name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// downloadFile fetches a URL and returns the body, with size limits.
// Retries up to 3 times on transient network errors (timeout, 5xx) so a
// single flaky request doesn't abort an install that touches dozens of files.
func (c *SkillsShClient) downloadFile(rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		body, err := c.downloadFileOnce(rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		// Don't retry hard 4xx (404 etc) — they won't get better.
		if strings.Contains(err.Error(), "HTTP 4") {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *SkillsShClient) downloadFileOnce(rawURL string) ([]byte, error) {
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

