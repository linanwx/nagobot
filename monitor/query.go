package monitor

import (
	"sort"
	"time"
)

// Window represents a time window for aggregation.
type Window string

const (
	Window1H Window = "1h"
	Window1D Window = "1d"
	Window7D Window = "7d"
)

func (w Window) Cutoff() time.Time {
	switch w {
	case Window1H:
		return time.Now().Add(-time.Hour)
	case Window1D:
		return time.Now().Add(-24 * time.Hour)
	case Window7D:
		return time.Now().Add(-7 * 24 * time.Hour)
	default:
		return time.Now().Add(-24 * time.Hour)
	}
}

// MetricsSummary is the top-level aggregation result.
type MetricsSummary struct {
	Window     string                    `json:"window" yaml:"window"`
	TotalTurns int                       `json:"totalTurns" yaml:"totalTurns"`
	AvgDurMs   int64                     `json:"avgDurationMs" yaml:"avgDurationMs"`
	AvgTokens  int                       `json:"avgTokens" yaml:"avgTokens"`
	ErrorRate  float64                   `json:"errorRate" yaml:"errorRate"`
	ByProvider map[string]*ProviderStats `json:"byProvider,omitempty" yaml:"byProvider,omitempty"`
	ByAgent    map[string]*GroupStats    `json:"byAgent,omitempty" yaml:"byAgent,omitempty"`
	BySession  map[string]*GroupStats    `json:"bySession,omitempty" yaml:"bySession,omitempty"`
}

// ProviderStats groups metrics by provider with model breakdown.
type ProviderStats struct {
	Turns    int                    `json:"turns" yaml:"turns"`
	AvgDurMs int64                  `json:"avgDurationMs" yaml:"avgDurationMs"`
	Models   map[string]*GroupStats `json:"models,omitempty" yaml:"models,omitempty"`
}

// GroupStats holds aggregated metrics for a group.
type GroupStats struct {
	Turns    int   `json:"turns" yaml:"turns"`
	AvgDurMs int64 `json:"avgDurationMs" yaml:"avgDurationMs"`
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
	}

	var totalDur int64
	var totalTokens int
	var errorCount int

	for _, r := range records {
		totalDur += r.DurationMs
		totalTokens += r.TotalTokens
		if r.Error {
			errorCount++
		}

		// By provider + model
		ps, ok := summary.ByProvider[r.Provider]
		if !ok {
			ps = &ProviderStats{Models: make(map[string]*GroupStats)}
			summary.ByProvider[r.Provider] = ps
		}
		ps.Turns++
		ps.AvgDurMs += r.DurationMs
		ms, ok := ps.Models[r.Model]
		if !ok {
			ms = &GroupStats{}
			ps.Models[r.Model] = ms
		}
		ms.Turns++
		ms.AvgDurMs += r.DurationMs

		// By agent
		if r.Agent != "" {
			as, ok := summary.ByAgent[r.Agent]
			if !ok {
				as = &GroupStats{}
				summary.ByAgent[r.Agent] = as
			}
			as.Turns++
			as.AvgDurMs += r.DurationMs
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
		}
	}

	n := int64(len(records))
	summary.AvgDurMs = totalDur / n
	if n > 0 {
		summary.AvgTokens = totalTokens / int(n)
	}
	if len(records) > 0 {
		summary.ErrorRate = float64(errorCount) / float64(len(records)) * 100
	}

	// Convert accumulated durations to averages
	for _, ps := range summary.ByProvider {
		if ps.Turns > 0 {
			ps.AvgDurMs /= int64(ps.Turns)
		}
		for _, ms := range ps.Models {
			if ms.Turns > 0 {
				ms.AvgDurMs /= int64(ms.Turns)
			}
		}
	}
	for _, as := range summary.ByAgent {
		if as.Turns > 0 {
			as.AvgDurMs /= int64(as.Turns)
		}
	}
	for _, ss := range summary.BySession {
		if ss.Turns > 0 {
			ss.AvgDurMs /= int64(ss.Turns)
		}
	}

	// Remove empty maps
	if len(summary.ByAgent) == 0 {
		summary.ByAgent = nil
	}
	if len(summary.BySession) == 0 {
		summary.BySession = nil
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
