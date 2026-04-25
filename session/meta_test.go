package session

import (
	"math"
	"testing"
)

func TestAppendTokenRatioSample_AppendsAndCaps(t *testing.T) {
	dir := t.TempDir()

	// Append MaxTokenRatioSamples + 5 samples — older ones should be evicted.
	for i := 0; i < MaxTokenRatioSamples+5; i++ {
		ratio := float64(i + 1) // 1.0, 2.0, ... distinct values to verify FIFO order
		AppendTokenRatioSample(dir, "openrouter", "claude-sonnet-4-6", ratio)
	}

	m := ReadMeta(dir)
	bucket := m.TokenEstimateRatios["openrouter/claude-sonnet-4-6"]
	if len(bucket) != MaxTokenRatioSamples {
		t.Fatalf("bucket size = %d, want %d", len(bucket), MaxTokenRatioSamples)
	}
	// Oldest retained sample should be the 6th appended (value 6.0); newest the 15th (15.0).
	if got, want := bucket[0].Ratio, 6.0; got != want {
		t.Errorf("oldest retained ratio = %v, want %v", got, want)
	}
	if got, want := bucket[len(bucket)-1].Ratio, 15.0; got != want {
		t.Errorf("newest ratio = %v, want %v", got, want)
	}
	for i, s := range bucket {
		if s.CreatedAt.IsZero() {
			t.Errorf("sample %d has zero CreatedAt", i)
		}
	}
}

func TestAppendTokenRatioSample_SeparatesByProviderModel(t *testing.T) {
	dir := t.TempDir()

	AppendTokenRatioSample(dir, "openrouter", "claude-sonnet-4-6", 1.05)
	AppendTokenRatioSample(dir, "deepseek", "deepseek-chat", 0.95)

	m := ReadMeta(dir)
	if len(m.TokenEstimateRatios) != 2 {
		t.Fatalf("buckets = %d, want 2", len(m.TokenEstimateRatios))
	}
	if r := m.TokenEstimateRatios["openrouter/claude-sonnet-4-6"]; len(r) != 1 || r[0].Ratio != 1.05 {
		t.Errorf("openrouter bucket = %v", r)
	}
	if r := m.TokenEstimateRatios["deepseek/deepseek-chat"]; len(r) != 1 || r[0].Ratio != 0.95 {
		t.Errorf("deepseek bucket = %v", r)
	}
}

func TestAppendTokenRatioSample_SkipsInvalid(t *testing.T) {
	dir := t.TempDir()

	// Empty fields → no-op.
	AppendTokenRatioSample("", "p", "m", 1.0)
	AppendTokenRatioSample(dir, "", "m", 1.0)
	AppendTokenRatioSample(dir, "p", "", 1.0)
	// Non-finite or non-positive ratios → no-op.
	AppendTokenRatioSample(dir, "p", "m", 0)
	AppendTokenRatioSample(dir, "p", "m", -1)
	AppendTokenRatioSample(dir, "p", "m", math.NaN())
	AppendTokenRatioSample(dir, "p", "m", math.Inf(1))

	m := ReadMeta(dir)
	if len(m.TokenEstimateRatios) != 0 {
		t.Fatalf("expected no buckets, got %v", m.TokenEstimateRatios)
	}
}
