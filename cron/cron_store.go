package cron

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

func (s *Scheduler) readStore() ([]Job, error) {
	if s.storePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var list []Job
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
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

	data, err := yaml.Marshal(list)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.storePath), 0o755); err != nil {
		return err
	}
	tmp := s.storePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.storePath)
}
