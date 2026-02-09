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

func (s *Scheduler) readStore() ([]Job, error) {
	if s.storePath == "" {
		return nil, nil
	}
	f, err := os.Open(s.storePath)
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

func (s *Scheduler) saveLocked() error {
	if s.storePath == "" {
		return nil
	}

	list := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		list = append(list, job)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	var buf bytes.Buffer
	for _, job := range list {
		data, err := json.Marshal(job)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	if err := os.MkdirAll(filepath.Dir(s.storePath), 0o755); err != nil {
		return err
	}
	tmp := s.storePath + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.storePath)
}
