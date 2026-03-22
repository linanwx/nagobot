package thread

import "testing"

func TestComputeContextThresholds(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		wantWarn      int
		wantTier2     int
	}{
		{"128K model", 128000, 25600, 46080},
		{"200K model", 200000, 40000, 72000},
		{"256K model", 256000, 50000, 90000},
		{"1M model", 1000000, 50000, 90000},
		{"small 32K model", 32000, 6400, 11520},
		{"zero", 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct := ComputeContextThresholds(tt.contextWindow)
			if ct.ContextWindow != tt.contextWindow {
				t.Errorf("ContextWindow = %d, want %d", ct.ContextWindow, tt.contextWindow)
			}
			if ct.WarnToken != tt.wantWarn {
				t.Errorf("WarnToken = %d, want %d", ct.WarnToken, tt.wantWarn)
			}
			if ct.Tier2Token != tt.wantTier2 {
				t.Errorf("Tier2Token = %d, want %d", ct.Tier2Token, tt.wantTier2)
			}
		})
	}
}

func TestPressureStatus(t *testing.T) {
	ct := ComputeContextThresholds(200000) // WarnToken=40000, Tier2Token=72000
	tests := []struct {
		name       string
		usedTokens int
		want       string
	}{
		{"ok - plenty of room", 50000, "ok"},
		{"warning - within tier2 zone", 140000, "warning"},
		{"pressure - remaining below warnToken", 170000, "pressure"},
		{"pressure - remaining exactly zero", 200000, "pressure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PressureStatus(tt.usedTokens, ct)
			if got != tt.want {
				t.Errorf("PressureStatus(%d, ct) = %q, want %q",
					tt.usedTokens, got, tt.want)
			}
		})
	}
}
