package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/linanwx/nagobot/provider"
)

const quotaFileName = "openai_quota.json"

// StoreQuota persists a quota snapshot to {dir}/openai_quota.json.
func StoreQuota(dir string, q *provider.Quota) error {
	if q == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create quota dir: %w", err)
	}
	data, err := json.Marshal(q)
	if err != nil {
		return fmt.Errorf("marshal quota: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, quotaFileName), data, 0644)
}

// LoadQuota reads the persisted quota snapshot from {dir}/openai_quota.json.
func LoadQuota(dir string) (*provider.Quota, error) {
	data, err := os.ReadFile(filepath.Join(dir, quotaFileName))
	if err != nil {
		return nil, err
	}
	var q provider.Quota
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("parse quota: %w", err)
	}
	return &q, nil
}
