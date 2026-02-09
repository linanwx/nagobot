package health

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	cronpkg "github.com/linanwx/nagobot/cron"
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

	f, err := os.Open(path)
	if err != nil {
		info.ParseError = err.Error()
		return info
	}
	defer f.Close()

	var jobs []cronpkg.Job
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var job cronpkg.Job
		if err := json.Unmarshal(line, &job); err != nil {
			info.ParseError = err.Error()
			return info
		}
		jobs = append(jobs, job)
	}
	if err := scanner.Err(); err != nil {
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
