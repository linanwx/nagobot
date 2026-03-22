package health

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanLogs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got := scanLogs(dir)
	if got != nil {
		t.Fatalf("expected nil for empty log dir, got %+v", got)
	}
}

func TestScanLogs_EmptyString(t *testing.T) {
	got := scanLogs("")
	if got != nil {
		t.Fatalf("expected nil for empty logsDir, got %+v", got)
	}
}

func TestScanLogs_NonexistentDir(t *testing.T) {
	got := scanLogs("/nonexistent/path/to/logs")
	if got != nil {
		t.Fatalf("expected nil for nonexistent dir, got %+v", got)
	}
}

func TestScanLogs_CountsWarnAndError(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	lines := ""
	for i := 0; i < 3; i++ {
		ts := now.Add(-time.Duration(i) * time.Hour)
		lines += fmt.Sprintf("time=%s level=WARN msg=\"warning %d\"\n", ts.Format(time.RFC3339Nano), i)
	}
	for i := 0; i < 2; i++ {
		ts := now.Add(-time.Duration(i) * time.Hour)
		lines += fmt.Sprintf("time=%s level=ERROR msg=\"error %d\"\n", ts.Format(time.RFC3339Nano), i)
	}
	if err := os.WriteFile(filepath.Join(dir, "nagobot.log"), []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}

	got := scanLogs(dir)
	if got == nil {
		t.Fatal("expected non-nil LogHealth")
	}
	if got.WarnCount != 3 {
		t.Errorf("WarnCount = %d, want 3", got.WarnCount)
	}
	if got.ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", got.ErrorCount)
	}
	if len(got.RecentWarnings) != 3 {
		t.Errorf("RecentWarnings len = %d, want 3", len(got.RecentWarnings))
	}
	if len(got.RecentErrors) != 2 {
		t.Errorf("RecentErrors len = %d, want 2", len(got.RecentErrors))
	}
}

func TestScanLogs_KeepsLast5(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	var lines string
	for i := 0; i < 10; i++ {
		ts := now.Add(-time.Duration(10-i) * time.Minute) // oldest first
		lines += fmt.Sprintf("time=%s level=ERROR msg=\"error %d\"\n", ts.Format(time.RFC3339Nano), i)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.log"), []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}

	got := scanLogs(dir)
	if got == nil {
		t.Fatal("expected non-nil LogHealth")
	}
	if got.ErrorCount != 10 {
		t.Errorf("ErrorCount = %d, want 10", got.ErrorCount)
	}
	if len(got.RecentErrors) != 5 {
		t.Errorf("RecentErrors len = %d, want 5", len(got.RecentErrors))
	}
	// The last 5 should be errors 5-9 (most recent).
	for i, line := range got.RecentErrors {
		expected := fmt.Sprintf("error %d", i+5)
		if !contains(line, expected) {
			t.Errorf("RecentErrors[%d] = %q, expected to contain %q", i, line, expected)
		}
	}
}

func TestScanLogs_FiltersOldEntries(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	lines := fmt.Sprintf("time=%s level=ERROR msg=\"old error\"\n", old.Format(time.RFC3339Nano))
	lines += fmt.Sprintf("time=%s level=ERROR msg=\"recent error\"\n", recent.Format(time.RFC3339Nano))
	lines += fmt.Sprintf("time=%s level=WARN msg=\"old warn\"\n", old.Format(time.RFC3339Nano))
	lines += fmt.Sprintf("time=%s level=WARN msg=\"recent warn\"\n", recent.Format(time.RFC3339Nano))

	if err := os.WriteFile(filepath.Join(dir, "nagobot.log"), []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}

	got := scanLogs(dir)
	if got == nil {
		t.Fatal("expected non-nil LogHealth")
	}
	if got.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", got.ErrorCount)
	}
	if got.WarnCount != 1 {
		t.Errorf("WarnCount = %d, want 1", got.WarnCount)
	}
}

func TestScanLogs_IgnoresInfoLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	lines := fmt.Sprintf("time=%s level=INFO msg=\"just info\"\n", now.Format(time.RFC3339Nano))
	lines += fmt.Sprintf("time=%s level=DEBUG msg=\"debug\"\n", now.Format(time.RFC3339Nano))

	if err := os.WriteFile(filepath.Join(dir, "nagobot.log"), []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}

	got := scanLogs(dir)
	if got != nil {
		t.Fatalf("expected nil for INFO/DEBUG only, got %+v", got)
	}
}

func TestScanLogs_IgnoresNonLogFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	content := fmt.Sprintf("time=%s level=ERROR msg=\"error in txt\"\n", now.Format(time.RFC3339Nano))

	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got := scanLogs(dir)
	if got != nil {
		t.Fatalf("expected nil for non-.log files, got %+v", got)
	}
}

func TestScanLogs_MultipleLogFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	lines1 := fmt.Sprintf("time=%s level=WARN msg=\"warn in file1\"\n", now.Format(time.RFC3339Nano))
	lines2 := fmt.Sprintf("time=%s level=ERROR msg=\"error in file2\"\n", now.Format(time.RFC3339Nano))

	if err := os.WriteFile(filepath.Join(dir, "a.log"), []byte(lines1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.log"), []byte(lines2), 0644); err != nil {
		t.Fatal(err)
	}

	got := scanLogs(dir)
	if got == nil {
		t.Fatal("expected non-nil LogHealth")
	}
	if got.WarnCount != 1 {
		t.Errorf("WarnCount = %d, want 1", got.WarnCount)
	}
	if got.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", got.ErrorCount)
	}
}

func TestScanLogs_TimezoneOffset(t *testing.T) {
	dir := t.TempDir()
	// Use a timestamp with timezone offset instead of Z.
	now := time.Now()
	ts := now.Format(time.RFC3339)
	lines := fmt.Sprintf("time=%s level=ERROR msg=\"tz error\"\n", ts)

	if err := os.WriteFile(filepath.Join(dir, "nagobot.log"), []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}

	got := scanLogs(dir)
	if got == nil {
		t.Fatal("expected non-nil LogHealth")
	}
	if got.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", got.ErrorCount)
	}
}

func TestParseLogTime(t *testing.T) {
	tests := []struct {
		line string
		zero bool
	}{
		{"time=2026-03-22T10:30:00.000Z level=WARN msg=\"test\"", false},
		{"time=2026-03-22T10:30:00.000+08:00 level=WARN msg=\"test\"", false},
		{"time=2026-03-22T10:30:00Z level=ERROR msg=\"test\"", false},
		{"no time here level=ERROR msg=\"test\"", true},
		{"time=invalid level=ERROR msg=\"test\"", true},
	}
	for _, tt := range tests {
		got := parseLogTime(tt.line)
		if tt.zero && !got.IsZero() {
			t.Errorf("parseLogTime(%q) = %v, want zero", tt.line, got)
		}
		if !tt.zero && got.IsZero() {
			t.Errorf("parseLogTime(%q) = zero, want non-zero", tt.line)
		}
	}
}

func TestAppendRecent(t *testing.T) {
	var dst []string
	for i := 0; i < 7; i++ {
		appendRecent(&dst, fmt.Sprintf("line %d", i))
	}
	if len(dst) != 5 {
		t.Fatalf("len = %d, want 5", len(dst))
	}
	// Should contain lines 2-6 (the last 5).
	for i, line := range dst {
		expected := fmt.Sprintf("line %d", i+2)
		if line != expected {
			t.Errorf("dst[%d] = %q, want %q", i, line, expected)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
