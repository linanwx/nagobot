package health

import (
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	cronpkg "github.com/linanwx/nagobot/cron"
	"gopkg.in/yaml.v3"
)

func inspectCronFile(path string) *CronInfo {
	info := &CronInfo{Path: path}

	stat, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			info.Exists = false
			return info
		}
		info.ParseError = err.Error()
		return info
	}

	info.Exists = true
	info.FileSizeBytes = stat.Size()
	info.UpdatedAt = stat.ModTime().Format(time.RFC3339)

	data, err := os.ReadFile(path)
	if err != nil {
		info.ParseError = err.Error()
		return info
	}

	var jobs []cronpkg.Job
	if err := yaml.Unmarshal(data, &jobs); err != nil {
		info.ParseError = err.Error()
		return info
	}

	sort.Slice(jobs, func(i, j int) bool {
		return strings.TrimSpace(jobs[i].ID) < strings.TrimSpace(jobs[j].ID)
	})

	info.JobsCount = len(jobs)
	summaries := make([]CronJobInfo, 0, len(jobs))
	for _, job := range jobs {
		kind := strings.TrimSpace(job.Kind)
		if kind == "" {
			if job.AtTime.IsZero() {
				kind = cronpkg.JobKindCron
			} else {
				kind = cronpkg.JobKindAt
			}
		}

		entry := CronJobInfo{
			ID:                strings.TrimSpace(job.ID),
			Kind:              strings.ToLower(kind),
			Expr:              strings.TrimSpace(job.Expr),
			Agent:             strings.TrimSpace(job.Agent),
			CreatorSessionKey: strings.TrimSpace(job.CreatorSessionKey),
			Enabled:           job.Enabled,
			Silent:            job.Silent,
		}
		if !job.AtTime.IsZero() {
			entry.AtTime = job.AtTime.Format(time.RFC3339)
		}

		summaries = append(summaries, entry)
	}
	info.Jobs = summaries
	return info
}
