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
	smitheryBaseURL = "https://registry.smithery.ai"
)

// SmitheryClient interacts with the Smithery skill registry.
type SmitheryClient struct {
	BaseURL string
	client  *http.Client
}

// NewSmitheryClient creates a client for the Smithery registry.
func NewSmitheryClient() *SmitheryClient {
	return &SmitheryClient{
		BaseURL: smitheryBaseURL,
		client:  &http.Client{Timeout: hubHTTPTimeout},
	}
}

// --- Smithery API response types ---

type smitherySearchResponse struct {
	Skills     []smitherySkillEntry `json:"skills"`
	Pagination struct {
		TotalCount int `json:"totalCount"`
	} `json:"pagination"`
}

type smitherySkillEntry struct {
	QualifiedName string `json:"qualifiedName"`
	Slug          string `json:"slug"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	Namespace     string `json:"namespace"`
	GitURL        string `json:"gitUrl"`
	Verified      bool   `json:"verified"`
}

// Resolve finds a skill by its qualified name (e.g. "anthropics/webapp-testing")
// and returns the gitUrl for downloading.
func (c *SmitheryClient) Resolve(qualifiedName string) (*smitherySkillEntry, error) {
	// Search by exact qualified name. The API returns fuzzy results,
	// so we filter for an exact match.
	endpoint := fmt.Sprintf("%s/skills?q=%s&limit=50",
		c.BaseURL, url.QueryEscape(qualifiedName))

	resp, err := c.client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Smithery registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Smithery registry returned %s", resp.Status)
	}

	var sr smitherySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("invalid Smithery response: %w", err)
	}

	// Find exact match by qualifiedName (case-insensitive).
	for i := range sr.Skills {
		if strings.EqualFold(sr.Skills[i].QualifiedName, qualifiedName) {
			return &sr.Skills[i], nil
		}
	}

	return nil, fmt.Errorf("skill %q not found on Smithery", qualifiedName)
}

// Install downloads a skill from Smithery and saves it to skillsDir/{skillName}/.
// The slug argument should be the qualified name (e.g. "anthropics/webapp-testing").
// The skill is saved under the short slug (e.g. "webapp-testing").
func (c *SmitheryClient) Install(qualifiedName string, skillsDir string) (skillName string, err error) {
	entry, err := c.Resolve(qualifiedName)
	if err != nil {
		return "", err
	}

	if entry.GitURL == "" {
		return "", fmt.Errorf("skill %q has no repository URL", qualifiedName)
	}

	// Parse gitUrl to derive raw download URLs.
	// gitUrl format: https://github.com/{owner}/{repo}/tree/{branch}/{path}
	rawBase, err := gitURLToRawBase(entry.GitURL)
	if err != nil {
		return "", fmt.Errorf("cannot parse git URL %q: %w", entry.GitURL, err)
	}

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
	// This is best-effort -- many skills don't have references.
	c.downloadReferences(entry.GitURL, tmpDir)

	// Determine the local skill name (short slug, last component of qualified name).
	skillName = entry.Slug
	if skillName == "" {
		parts := strings.Split(qualifiedName, "/")
		skillName = parts[len(parts)-1]
	}

	// Atomic swap.
	destDir := filepath.Join(skillsDir, skillName)
	os.RemoveAll(destDir)
	if err := os.Rename(tmpDir, destDir); err != nil {
		return "", err
	}

	return skillName, nil
}

// gitURLToRawBase converts a GitHub tree URL to a raw.githubusercontent.com base URL.
// Input:  https://github.com/{owner}/{repo}/tree/{branch}/{path}
// Output: https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{path}
func gitURLToRawBase(gitURL string) (string, error) {
	u, err := url.Parse(gitURL)
	if err != nil {
		return "", err
	}

	if u.Host != "github.com" {
		return "", fmt.Errorf("unsupported host %q, only github.com is supported", u.Host)
	}

	// Path: /{owner}/{repo}/tree/{branch}/{path...}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 5)
	if len(parts) < 4 || parts[2] != "tree" {
		return "", fmt.Errorf("unexpected GitHub URL format: %s", gitURL)
	}

	owner := parts[0]
	repo := parts[1]
	branch := parts[3]
	subpath := ""
	if len(parts) >= 5 {
		subpath = "/" + parts[4]
	}

	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s%s",
		owner, repo, branch, subpath), nil
}

// gitURLToContentsAPI converts a GitHub tree URL to a GitHub Contents API URL.
// Input:  https://github.com/{owner}/{repo}/tree/{branch}/{path}
// Output: https://api.github.com/repos/{owner}/{repo}/contents/{path}?ref={branch}
func gitURLToContentsAPI(gitURL string) (string, error) {
	u, err := url.Parse(gitURL)
	if err != nil {
		return "", err
	}

	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 5)
	if len(parts) < 4 || parts[2] != "tree" {
		return "", fmt.Errorf("unexpected GitHub URL format: %s", gitURL)
	}

	owner := parts[0]
	repo := parts[1]
	branch := parts[3]
	subpath := ""
	if len(parts) >= 5 {
		subpath = parts[4]
	}

	return fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s/references?ref=%s",
		owner, repo, subpath, branch), nil
}

// downloadFile fetches a URL and returns the body, with size limits.
func (c *SmitheryClient) downloadFile(rawURL string) ([]byte, error) {
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

// downloadReferences attempts to download files from a references/ subdirectory
// using the GitHub Contents API. This is best-effort.
func (c *SmitheryClient) downloadReferences(gitURL string, destDir string) {
	contentsURL, err := gitURLToContentsAPI(gitURL)
	if err != nil {
		return
	}

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
