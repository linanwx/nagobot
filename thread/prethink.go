package thread

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
)

const (
	preThinkTimeout     = 10 * time.Second
	preThinkChatEntries = 16 // recent chat.jsonl lines passed in the pre-think markdown body
)

// isPreThinkSession reports whether the session key is a pre-think sibling.
func isPreThinkSession(key string) bool {
	return strings.HasSuffix(key, session.PreThinkSessionSuffix)
}

// fastModelConfigured reports whether the "fast" specialty is mapped to a
// concrete provider/model in the current config (with hot-reload).
func fastModelConfigured(cfg *ThreadConfig) bool {
	rules := cfg.Models
	if cfg.ModelsFn != nil {
		rules = cfg.ModelsFn()
	}
	return config.FindModelRule(rules, config.ModelRuleSpecialty, "fast") != nil
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

	// The pre-think session is stateless: it declares tier_lossy_mode: stateless
	// in its frontmatter, so the framework clears it at the start of every
	// pre-think turn (see clearIfStateless). No special-casing needed here.
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

// String fields carry text (omitted by the agent when empty).
var (
	confusingTermRE = regexp.MustCompile(`(?is)<confusing_terminology>(.*?)</confusing_terminology>`)
	hallucinationRE = regexp.MustCompile(`(?is)<hallucination>(.*?)</hallucination>`)
	searchRE        = regexp.MustCompile(`(?is)<search>(.*?)</search>`)
	skillRE         = regexp.MustCompile(`(?is)<skill>(.*?)</skill>`)
	toneRE          = regexp.MustCompile(`(?is)<tone>(.*?)</tone>`)
)

// Bool fields are presence-based true/false flags (omitted by the agent when
// false).
var (
	multiStepRE    = regexp.MustCompile(`(?is)<is_multi_step>(.*?)</is_multi_step>`)
	investigatorRE = regexp.MustCompile(`(?is)<is_include_investigator>(.*?)</is_include_investigator>`)
	hasWebURLRE    = regexp.MustCompile(`(?is)<has_web_url>(.*?)</has_web_url>`)
)

// stringTag returns the cleaned body of a string field, or "" if absent/empty.
func stringTag(re *regexp.Regexp, raw string) string {
	if m := re.FindStringSubmatch(raw); len(m) == 2 {
		return cleanTagBody(m[1])
	}
	return ""
}

// boolTag reports whether a presence-based bool field is true. Absent tags and
// explicit false/no/0/empty bodies count as false; any other body counts as
// true (the agent is told to omit false tags, so presence usually means true).
func boolTag(re *regexp.Regexp, raw string) bool {
	m := re.FindStringSubmatch(raw)
	if len(m) != 2 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(m[1])) {
	case "", "false", "no", "0":
		return false
	default:
		return true
	}
}

// parsePreThinkXML extracts the bool/string fields from pre-think output and
// composes a clean action hint. A non-empty <confusing_terminology> tag adds a
// mandatory clarification step; an <is_include_investigator> flag forces an
// explicit dispatch. Returns "" when no recognizable tags are found (caller
// falls back to raw).
func parsePreThinkXML(raw string) string {
	if raw == "" {
		return ""
	}

	var parts []string

	if boolTag(multiStepRE, raw) {
		parts = append(parts, "Multi-step task: plan the steps and complete all of them before responding.")
	}

	if v := stringTag(confusingTermRE, raw); v != "" {
		parts = append(parts, "Confusing terminology: "+v+". Before continuing, ask the user to clarify via dispatch(to=user) — a structured question with concrete options and their consequences — then wait for their answer.")
	}

	if v := stringTag(hallucinationRE, raw); v != "" {
		parts = append(parts, "Possible hallucination: "+v+". Consider searching references before continuing.")
	}

	if v := stringTag(searchRE, raw); v != "" {
		parts = append(parts, "Search: "+v+". Consider dispatching a search subagent.")
	}

	if boolTag(investigatorRE, raw) {
		parts = append(parts, "Investigator: you must call dispatch to fan out an investigator subagent before responding to the user.")
	}

	if boolTag(hasWebURLRE, raw) {
		parts = append(parts, "Web URL present: consider using playwright to open it.")
	}

	if v := stringTag(skillRE, raw); v != "" {
		parts = append(parts, "Related skill: "+v+". Consider use_skill(\""+v+"\") to load its instructions before proceeding.")
	}

	if v := stringTag(toneRE, raw); v != "" {
		parts = append(parts, "Tone: "+v+".")
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
