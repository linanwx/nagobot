package monitor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Window represents a time window for aggregation.
type Window string

const (
	Window1H Window = "1h"
	Window1D Window = "1d"
	Window7D Window = "7d"
)

// Cutoff returns the start time for this window. Invalid windows fall back to
// 24h; callers that want to reject bad input should validate via Duration first
// (the CLI does, so a typo surfaces an error instead of silently using 1d).
func (w Window) Cutoff() time.Time {
	d, err := w.Duration()
	if err != nil {
		return time.Now().Add(-24 * time.Hour)
	}
	return time.Now().Add(-d)
}

// Duration parses the window into a time.Duration. It accepts Go duration syntax
// (e.g. "90m", "12h", "1h30m") plus single-unit day ("7d") and week ("2w") forms
// that time.ParseDuration does not support. Returns an error for unparseable or
// non-positive windows so callers never silently fall back to a wrong span.
func (w Window) Duration() (time.Duration, error) {
	s := strings.TrimSpace(strings.ToLower(string(w)))
	if s == "" {
		return 0, fmt.Errorf("empty window")
	}
	var mult time.Duration
	switch {
	case strings.HasSuffix(s, "d"):
		mult = 24 * time.Hour
	case strings.HasSuffix(s, "w"):
		mult = 7 * 24 * time.Hour
	}
	if mult > 0 {
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid window %q (use forms like 90m, 12h, 7d, 2w)", string(w))
		}
		d := time.Duration(n * float64(mult))
		if d <= 0 {
			return 0, fmt.Errorf("window must be positive: %q", string(w))
		}
		return d, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid window %q (use forms like 90m, 12h, 7d, 2w)", string(w))
	}
	if d <= 0 {
		return 0, fmt.Errorf("window must be positive: %q", string(w))
	}
	return d, nil
}

// MetricsSummary is the top-level aggregation result.
type MetricsSummary struct {
	Window     string                    `json:"window" yaml:"window"`
	DataRange  string                    `json:"dataRange,omitempty" yaml:"dataRange,omitempty"` // actual span of records included (may be narrower than Window if data starts later)
	TotalTurns int                       `json:"totalTurns" yaml:"totalTurns"`
	AvgDurMs   int64                     `json:"avgDurationMs" yaml:"avgDurationMs"`
	AvgTokens  int                       `json:"avgTokens" yaml:"avgTokens"`
	ErrorRate  string                    `json:"errorRate" yaml:"errorRate"` // formatted percentage, e.g. "0.9%"
	ByProvider map[string]*ProviderStats `json:"byProvider,omitempty" yaml:"byProvider,omitempty"`
	ByAgent    map[string]*GroupStats    `json:"byAgent,omitempty" yaml:"byAgent,omitempty"`
	BySession  map[string]*GroupStats    `json:"bySession,omitempty" yaml:"bySession,omitempty"`
	BySource   map[string]*GroupStats    `json:"bySource,omitempty" yaml:"bySource,omitempty"`
}

// ProviderStats groups metrics by provider with model breakdown.
type ProviderStats struct {
	Turns                     int                    `json:"turns" yaml:"turns"`
	AvgDurMs                  int64                  `json:"avgDurationMs" yaml:"avgDurationMs"`
	PromptTokens              int                    `json:"promptTokens" yaml:"promptTokens"`
	CachedTokens              int                    `json:"cachedTokens" yaml:"cachedTokens"`
	CacheWriteTokens          int                    `json:"cacheWriteTokens,omitempty" yaml:"cacheWriteTokens,omitempty"`
	CacheEligiblePromptTokens int                    `json:"cacheEligiblePromptTokens,omitempty" yaml:"cacheEligiblePromptTokens,omitempty"`
	CacheHitRate              string                 `json:"cacheHitRate" yaml:"cacheHitRate"`
	Models                    map[string]*GroupStats `json:"models,omitempty" yaml:"models,omitempty"`
}

// isCacheUnreliable returns true for providers that don't reliably return cached_tokens.
func isCacheUnreliable(providerName string) bool {
	return strings.Contains(providerName, "openai-oauth")
}

// GroupStats holds aggregated metrics for a group.
type GroupStats struct {
	Turns            int   `json:"turns" yaml:"turns"`
	AvgDurMs         int64 `json:"avgDurationMs" yaml:"avgDurationMs"`
	PromptTokens     int   `json:"promptTokens" yaml:"promptTokens"`
	CompletionTokens int   `json:"completionTokens,omitempty" yaml:"completionTokens,omitempty"`
	CachedTokens     int   `json:"cachedTokens" yaml:"cachedTokens"`
	// CacheWriteTokens is summed for every provider, unlike CachedTokens, which
	// is gated on cacheReliable. A raw count needs no matched denominator, so an
	// unreliable provider cannot skew it — and omitempty keeps it out of the
	// output for the providers that never report it.
	CacheWriteTokens          int    `json:"cacheWriteTokens,omitempty" yaml:"cacheWriteTokens,omitempty"`
	CacheEligiblePromptTokens int    `json:"cacheEligiblePromptTokens,omitempty" yaml:"cacheEligiblePromptTokens,omitempty"`
	CacheHitRate              string `json:"cacheHitRate" yaml:"cacheHitRate"`
}

// Query aggregates turn records for the given time window.
func Query(store *Store, window Window) *MetricsSummary {
	records := store.Load(window.Cutoff())
	if len(records) == 0 {
		return &MetricsSummary{Window: string(window)}
	}

	summary := &MetricsSummary{
		Window:     string(window),
		TotalTurns: len(records),
		ByProvider: make(map[string]*ProviderStats),
		ByAgent:    make(map[string]*GroupStats),
		BySession:  make(map[string]*GroupStats),
		BySource:   make(map[string]*GroupStats),
	}

	var totalDur int64
	var totalTokens int
	var errorCount int
	var earliest, latest time.Time

	for _, r := range records {
		totalDur += r.DurationMs
		totalTokens += r.AccTotalTokens
		if r.Error {
			errorCount++
		}
		if !r.Timestamp.IsZero() {
			if earliest.IsZero() || r.Timestamp.Before(earliest) {
				earliest = r.Timestamp
			}
			if r.Timestamp.After(latest) {
				latest = r.Timestamp
			}
		}

		cacheReliable := !isCacheUnreliable(r.Provider)

		// By provider + model
		ps, ok := summary.ByProvider[r.Provider]
		if !ok {
			ps = &ProviderStats{Models: make(map[string]*GroupStats)}
			summary.ByProvider[r.Provider] = ps
		}
		ps.Turns++
		ps.AvgDurMs += r.DurationMs
		ps.PromptTokens += r.AccPromptTokens
		ps.CacheWriteTokens += r.AccCacheWriteTokens
		// Cache numerator and denominator must come from the same turn set:
		// only count cached + eligible tokens for cache-reliable providers.
		// Otherwise an unreliable provider's cached tokens (counted) without
		// its eligible tokens (skipped) push the ratio past 100%.
		if cacheReliable {
			ps.CachedTokens += r.AccCachedTokens
			ps.CacheEligiblePromptTokens += r.AccPromptTokens
		}
		ms, ok := ps.Models[r.Model]
		if !ok {
			ms = &GroupStats{}
			ps.Models[r.Model] = ms
		}
		ms.Turns++
		ms.AvgDurMs += r.DurationMs
		ms.PromptTokens += r.AccPromptTokens
		ms.CacheWriteTokens += r.AccCacheWriteTokens
		ms.CompletionTokens += r.AccCompletionTokens
		if cacheReliable {
			ms.CachedTokens += r.AccCachedTokens
			ms.CacheEligiblePromptTokens += r.AccPromptTokens
		}

		// By agent
		if r.Agent != "" {
			as, ok := summary.ByAgent[r.Agent]
			if !ok {
				as = &GroupStats{}
				summary.ByAgent[r.Agent] = as
			}
			as.Turns++
			as.AvgDurMs += r.DurationMs
			as.PromptTokens += r.AccPromptTokens
			as.CacheWriteTokens += r.AccCacheWriteTokens
			as.CompletionTokens += r.AccCompletionTokens
			if cacheReliable {
				as.CachedTokens += r.AccCachedTokens
				as.CacheEligiblePromptTokens += r.AccPromptTokens
			}
		}

		// By session
		if r.SessionKey != "" {
			ss, ok := summary.BySession[r.SessionKey]
			if !ok {
				ss = &GroupStats{}
				summary.BySession[r.SessionKey] = ss
			}
			ss.Turns++
			ss.AvgDurMs += r.DurationMs
			ss.PromptTokens += r.AccPromptTokens
			ss.CacheWriteTokens += r.AccCacheWriteTokens
			ss.CompletionTokens += r.AccCompletionTokens
			if cacheReliable {
				ss.CachedTokens += r.AccCachedTokens
				ss.CacheEligiblePromptTokens += r.AccPromptTokens
			}
		}

		// By wake source
		if r.Source != "" {
			src, ok := summary.BySource[r.Source]
			if !ok {
				src = &GroupStats{}
				summary.BySource[r.Source] = src
			}
			src.Turns++
			src.AvgDurMs += r.DurationMs
			src.PromptTokens += r.AccPromptTokens
			src.CacheWriteTokens += r.AccCacheWriteTokens
			src.CompletionTokens += r.AccCompletionTokens
			if cacheReliable {
				src.CachedTokens += r.AccCachedTokens
				src.CacheEligiblePromptTokens += r.AccPromptTokens
			}
		}
	}

	n := int64(len(records))
	summary.AvgDurMs = totalDur / n
	if n > 0 {
		summary.AvgTokens = totalTokens / int(n)
	}
	if !earliest.IsZero() {
		const layout = "2006-01-02 15:04"
		summary.DataRange = earliest.Local().Format(layout) + " → " + latest.Local().Format(layout)
	}
	if len(records) > 0 {
		summary.ErrorRate = fmt.Sprintf("%.1f%%", float64(errorCount)/float64(len(records))*100)
	} else {
		summary.ErrorRate = "0.0%"
	}

	// Convert accumulated durations to averages and compute cache hit rates.
	// Cache hit rate uses CacheEligiblePromptTokens (only from reliable providers)
	// as denominator. If no eligible tokens, show N/A.
	computeCacheRate := func(cachedTokens, eligiblePromptTokens int) string {
		if eligiblePromptTokens <= 0 {
			return "N/A"
		}
		return fmt.Sprintf("%.1f%%", float64(cachedTokens)/float64(eligiblePromptTokens)*100)
	}
	finalizeGroup := func(g *GroupStats) {
		if g.Turns > 0 {
			g.AvgDurMs /= int64(g.Turns)
		}
		g.CacheHitRate = computeCacheRate(g.CachedTokens, g.CacheEligiblePromptTokens)
	}
	for _, ps := range summary.ByProvider {
		if ps.Turns > 0 {
			ps.AvgDurMs /= int64(ps.Turns)
		}
		ps.CacheHitRate = computeCacheRate(ps.CachedTokens, ps.CacheEligiblePromptTokens)
		for _, ms := range ps.Models {
			finalizeGroup(ms)
		}
	}
	for _, as := range summary.ByAgent {
		finalizeGroup(as)
	}
	for _, ss := range summary.BySession {
		finalizeGroup(ss)
	}
	for _, src := range summary.BySource {
		finalizeGroup(src)
	}

	// Remove empty maps
	if len(summary.ByAgent) == 0 {
		summary.ByAgent = nil
	}
	if len(summary.BySession) == 0 {
		summary.BySession = nil
	}
	if len(summary.BySource) == 0 {
		summary.BySource = nil
	}

	return summary
}

// RecentTurns returns the most recent N turn records.
func RecentTurns(store *Store, n int) []TurnRecord {
	records := store.Load(time.Time{})
	if len(records) <= n {
		return records
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})
	return records[:n]
}
