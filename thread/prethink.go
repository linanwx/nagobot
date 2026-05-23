package thread

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

const (
	preThinkTimeout     = 10 * time.Second
	preThinkChatEntries = 16 // recent chat.jsonl lines passed in the pre-think YAML header
)

// isPreThinkSession reports whether the session key is a pre-think sibling.
func isPreThinkSession(key string) bool {
	return strings.HasSuffix(key, session.PreThinkSessionSuffix)
}

// fastModelConfigured reports whether the "fast" specialty is mapped to a
// concrete provider/model in the current config (with hot-reload).
func fastModelConfigured(cfg *ThreadConfig) bool {
	models := cfg.Models
	if cfg.ModelsFn != nil {
		models = cfg.ModelsFn()
	}
	mc, ok := models["fast"]
	return ok && mc != nil
}

// preThinkAction runs the pre-think agent synchronously and returns its
// analysis to use as the action hint for the main wake payload.
// Returns "" if pre-think is not configured, the input is empty, or it fails.
//
// Caller must guard with sysmsg.IsUserVisibleSource — pre-think only runs
// for real user messages, not system wakes.
func preThinkAction(ctx context.Context, t *Thread, userMsg string) string {
	if strings.TrimSpace(userMsg) == "" {
		return ""
	}
	if isPreThinkSession(t.sessionKey) || isRephraseSession(t.sessionKey) {
		return ""
	}
	if !fastModelConfigured(t.cfg()) {
		return ""
	}

	ch := make(chan string, 1)
	preThinkKey := t.sessionKey + session.PreThinkSessionSuffix
	recentChat := session.ReadRecentChat(t.mgr.SessionDir(t.sessionKey), preThinkChatEntries)
	t.mgr.Wake(preThinkKey, &WakeMessage{
		Source:     WakePreThink,
		Message:    userMsg,
		AgentName:  "pre-think",
		RecentChat: recentChat,
		OnComplete: func(response string) {
			ch <- response
		},
	})

	select {
	case result := <-ch:
		result = strings.TrimSpace(result)
		parsed := parsePreThinkXML(result)
		if parsed != "" {
			logger.Info("pre-think completed",
				"sessionKey", t.sessionKey,
				"rawLen", len(result),
				"parsedLen", len(parsed),
			)
			return parsed
		}
		if result != "" {
			logger.Warn("pre-think XML parse fallback to raw",
				"sessionKey", t.sessionKey,
				"resultLen", len(result),
			)
		}
		return result
	case <-time.After(preThinkTimeout):
		logger.Warn("pre-think timeout", "sessionKey", t.sessionKey)
		return ""
	case <-ctx.Done():
		return ""
	}
}

var (
	intentRE = regexp.MustCompile(`(?is)<intent>(.*?)</intent>`)
	searchRE = regexp.MustCompile(`(?is)<search>(.*?)</search>`)
	fanoutRE = regexp.MustCompile(`(?is)<fanout>(.*?)</fanout>`)
	toneRE   = regexp.MustCompile(`(?is)<tone>(.*?)</tone>`)
	riskRE   = regexp.MustCompile(`(?is)<risk\s+name="([^"]+)"\s+level="([^"]+)"\s*>(.*?)</risk>`)
)

// parsePreThinkXML extracts structured fields from pre-think output and
// composes a clean action hint. Risk tags with level="low" are filtered out.
// Returns "" when no recognizable tags are found (caller falls back to raw).
func parsePreThinkXML(raw string) string {
	if raw == "" {
		return ""
	}

	var parts []string

	if m := intentRE.FindStringSubmatch(raw); len(m) == 2 {
		if v := cleanTagBody(m[1]); v != "" {
			parts = append(parts, "Intent: "+v+".")
		}
	}

	for _, m := range riskRE.FindAllStringSubmatch(raw, -1) {
		name := strings.ToLower(strings.TrimSpace(m[1]))
		level := strings.ToLower(strings.TrimSpace(m[2]))
		reason := cleanTagBody(m[3])
		if level != "medium" && level != "high" {
			continue
		}
		if name == "" {
			continue
		}
		entry := strings.ToUpper(level[:1]) + level[1:] + " " + name + " risk"
		if reason != "" {
			entry += ": " + reason
		}
		parts = append(parts, entry+".")
	}

	if m := searchRE.FindStringSubmatch(raw); len(m) == 2 {
		if v := cleanTagBody(m[1]); v != "" {
			parts = append(parts, "Search: "+v+".")
		}
	}

	if m := fanoutRE.FindStringSubmatch(raw); len(m) == 2 {
		if v := cleanTagBody(m[1]); v != "" {
			parts = append(parts, "Fanout: "+v+".")
		}
	}

	if m := toneRE.FindStringSubmatch(raw); len(m) == 2 {
		if v := cleanTagBody(m[1]); v != "" {
			parts = append(parts, "Tone: "+v+".")
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func cleanTagBody(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimRight(s, ".")
	return s
}
