package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// searchRecord is a single search request outcome.
type searchRecord struct {
	Source    string    `json:"s"`
	OK       bool      `json:"ok"`
	Results  int       `json:"n"`
	Ms       int64     `json:"ms"`
	Time     time.Time `json:"t"`
}

// SearchHealthChecker tracks real search request outcomes (no active probing).
type SearchHealthChecker struct {
	providers map[string]SearchProvider

	mu      sync.Mutex
	records []searchRecord

	persistPath string
}

// NewSearchHealthChecker creates a health checker backed by real usage data.
func NewSearchHealthChecker(providers map[string]SearchProvider) *SearchHealthChecker {
	return &SearchHealthChecker{
		providers: providers,
	}
}

// SetPersistPath sets the file path for persisting records.
// Call before any Record() calls.
func (h *SearchHealthChecker) SetPersistPath(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.persistPath = path
	h.load()
}

// Record logs a search request outcome.
func (h *SearchHealthChecker) Record(source string, ok bool, results int, ms int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, searchRecord{
		Source:  source,
		OK:      ok,
		Results: results,
		Ms:      ms,
		Time:    time.Now(),
	})
	h.gc()
	h.save()
}

// StatusSummary returns a compact one-line summary for tool result YAML headers.
func (h *SearchHealthChecker) StatusSummary() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buildSummary(24 * time.Hour)
}

// DetailedStatus returns verbose status for error scenarios (7d + 24h).
func (h *SearchHealthChecker) DetailedStatus() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	names := h.sourceNames()
	if len(names) == 0 {
		return "No search data recorded yet.\n"
	}

	var sb strings.Builder
	sb.WriteString("Search provider stats:\n")
	sb.WriteString("  [24h] ")
	sb.WriteString(h.buildSummary(24 * time.Hour))
	sb.WriteString("\n  [7d]  ")
	sb.WriteString(h.buildSummary(7 * 24 * time.Hour))
	sb.WriteString("\n")
	return sb.String()
}

// buildSummary builds a compact summary for the given window. Caller holds mu.
func (h *SearchHealthChecker) buildSummary(window time.Duration) string {
	cutoff := time.Now().Add(-window)
	type stats struct {
		ok, fail     int
		totalResults int
		totalMs      int64
	}
	m := make(map[string]*stats)
	for _, r := range h.records {
		if r.Time.Before(cutoff) {
			continue
		}
		s := m[r.Source]
		if s == nil {
			s = &stats{}
			m[r.Source] = s
		}
		if r.OK {
			s.ok++
			s.totalResults += r.Results
			s.totalMs += r.Ms
		} else {
			s.fail++
		}
	}

	names := h.sourceNames()
	var parts []string
	for _, name := range names {
		tags := h.tagsFor(name)
		s := m[name]
		if s == nil {
			parts = append(parts, fmt.Sprintf("%s%s: not used yet", name, tags))
			continue
		}
		total := s.ok + s.fail
		if s.ok == 0 {
			parts = append(parts, fmt.Sprintf("%s%s: all %d requests failed", name, tags, total))
			continue
		}
		avgMs := s.totalMs / int64(s.ok)
		avgResults := s.totalResults / s.ok
		if s.fail == 0 {
			parts = append(parts, fmt.Sprintf("%s%s: %d requests, avg %d results, %dms",
				name, tags, total, avgResults, avgMs))
		} else {
			parts = append(parts, fmt.Sprintf("%s%s: %d/%d succeeded, avg %d results, %dms",
				name, tags, s.ok, total, avgResults, avgMs))
		}
	}
	return strings.Join(parts, "; ")
}

// sourceNames returns sorted provider names. Caller holds mu.
func (h *SearchHealthChecker) sourceNames() []string {
	names := make([]string, 0, len(h.providers))
	for n, p := range h.providers {
		if p.Available() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// tagsFor returns formatted tags string for a provider, e.g. " [free]". Caller holds mu.
func (h *SearchHealthChecker) tagsFor(name string) string {
	p, ok := h.providers[name]
	if !ok {
		return ""
	}
	tags := p.Tags()
	if len(tags) == 0 {
		return ""
	}
	return " [" + strings.Join(tags, ",") + "]"
}

// gc removes records older than 7 days. Caller holds mu.
func (h *SearchHealthChecker) gc() {
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	i := 0
	for i < len(h.records) && h.records[i].Time.Before(cutoff) {
		i++
	}
	if i > 0 {
		h.records = h.records[i:]
	}
}

// load reads persisted records from disk. Caller holds mu.
func (h *SearchHealthChecker) load() {
	if h.persistPath == "" {
		return
	}
	data, err := os.ReadFile(h.persistPath)
	if err != nil {
		return
	}
	var records []searchRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return
	}
	h.records = records
	h.gc()
}

// save writes records to disk. Caller holds mu.
func (h *SearchHealthChecker) save() {
	if h.persistPath == "" {
		return
	}
	data, err := json.Marshal(h.records)
	if err != nil {
		return
	}
	dir := filepath.Dir(h.persistPath)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(h.persistPath, data, 0o644)
}
