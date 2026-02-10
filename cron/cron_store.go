package cron

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// ReadJobs reads all jobs from a JSONL file.
// Returns nil slice (not error) if the file does not exist.
func ReadJobs(path string) ([]Job, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var list []Job
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var job Job
		if err := json.Unmarshal(line, &job); err != nil {
			return nil, err
		}
		list = append(list, job)
	}
	return list, scanner.Err()
}

// WriteJobs writes jobs to a JSONL file atomically (tmp + rename).
// Jobs are sorted by ID before writing.
func WriteJobs(path string, jobs []Job) error {
	sorted := make([]Job, len(jobs))
	copy(sorted, jobs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var buf bytes.Buffer
	for _, job := range sorted {
		data, err := json.Marshal(job)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Scheduler) readStore() ([]Job, error) {
	if s.storePath == "" {
		return nil, nil
	}
	return ReadJobs(s.storePath)
}

func (s *Scheduler) saveLocked() error {
	if s.storePath == "" {
		return nil
	}
	list := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		list = append(list, job)
	}
	return WriteJobs(s.storePath, list)
}
