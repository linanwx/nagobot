package cron

import (
	"path/filepath"
	"testing"
)

func TestSchedulerAddLoadRemove(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "cron.json")

	s := NewScheduler(storePath, nil)
	if err := s.Add("daily-report", "@daily", "generate and send report", "cron"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 job, got %d", len(list))
	}
	if list[0].ID != "daily-report" {
		t.Fatalf("unexpected job id: %s", list[0].ID)
	}

	loaded := NewScheduler(storePath, nil)
	if err := loaded.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	loadedList := loaded.List()
	if len(loadedList) != 1 {
		t.Fatalf("expected 1 loaded job, got %d", len(loadedList))
	}
	if loadedList[0].Expr != "@daily" {
		t.Fatalf("unexpected expr: %s", loadedList[0].Expr)
	}

	if err := loaded.Remove("daily-report"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if got := len(loaded.List()); got != 0 {
		t.Fatalf("expected 0 jobs after remove, got %d", got)
	}
}
