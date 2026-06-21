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
	if isPreThinkSession(t.sessionKey) {
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
	recentChat := session.ReadRecentChat(t.mgr.SessionDir(t.sessionKey), preThinkChatEntries, t.location())
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
	skillsRE = regexp.MustCompile(`(?is)<skills?>(.*?)</skills?>`)
	toneRE   = regexp.MustCompile(`(?is)<tone>(.*?)</tone>`)
)

// Bool fields are presence-based true/false flags (omitted by the agent when
// false).
var (
	multiStepRE         = regexp.MustCompile(`(?is)<is_multi_step>(.*?)</is_multi_step>`)
	confusingTermRE     = regexp.MustCompile(`(?is)<confusing_terminology>(.*?)</confusing_terminology>`)
	hallucinationRE     = regexp.MustCompile(`(?is)<hallucination>(.*?)</hallucination>`)
	searchRE            = regexp.MustCompile(`(?is)<search>(.*?)</search>`)
	investigatorRE      = regexp.MustCompile(`(?is)<is_include_investigator>(.*?)</is_include_investigator>`)
	hasWebURLRE         = regexp.MustCompile(`(?is)<has_web_url>(.*?)</has_web_url>`)
	destructiveRE       = regexp.MustCompile(`(?is)<destructive>(.*?)</destructive>`)
	needsVerificationRE = regexp.MustCompile(`(?is)<needs_verification>(.*?)</needs_verification>`)
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
// composes a clean action hint. A true <confusing_terminology> flag adds an
// investigate-then-ask clarification step; <destructive> warns before
// irreversible actions; <needs_verification> primes a post-action check; an
// <is_include_investigator> flag forces an explicit dispatch. Returns "" when no
// recognizable tags are found (caller falls back to raw).
func parsePreThinkXML(raw string) string {
	if raw == "" {
		return ""
	}

	var parts []string

	if boolTag(multiStepRE, raw) {
		parts = append(parts, "Multi-step task: plan the steps and complete all of them before responding.")
	}

	if boolTag(confusingTermRE, raw) {
		parts = append(parts, "Needs clarification: the request is ambiguous or lacks enough context to answer on-target. First try to resolve it from memory, session history, or a search; only ask the user via dispatch(to=user) — a structured question with concrete options and their consequences — for genuine preferences or trade-offs that investigation cannot settle.")
	}

	if boolTag(destructiveRE, raw) {
		parts = append(parts, "Destructive action: fulfilling this may delete data, send/publish to others, write outside the workspace, or trigger irreversible side effects. Confirm with the user via dispatch(to=user) before executing, and prefer a dry-run or reversible path.")
	}

	if boolTag(hallucinationRE, raw) {
		parts = append(parts, "Possible hallucination: there is a meaningful chance (>10%) the model would misremember a fact here (versions, specs, prices, dates, people-in-roles). Verify against a source before stating it.")
	}

	if boolTag(searchRE, raw) {
		parts = append(parts, "Search: there is a meaningful chance (>10%) a relevant fact has changed since the model's training cutoff or needs an authoritative source. Consider dispatching a search subagent.")
	}

	if boolTag(investigatorRE, raw) {
		parts = append(parts, "Investigator: you must call dispatch to fan out an investigator subagent before responding to the user.")
	}

	if boolTag(hasWebURLRE, raw) {
		parts = append(parts, "Web URL present: consider using playwright to open it.")
	}

	if boolTag(needsVerificationRE, raw) {
		parts = append(parts, "Needs verification: this task produces a change whose correctness should be confirmed by running or observing it (code, config, deployment), not by reasoning alone. Verify by observing actual behavior after acting.")
	}

	if slugs := splitSkillSlugs(stringTag(skillsRE, raw)); len(slugs) > 0 {
		calls := make([]string, len(slugs))
		for i, s := range slugs {
			calls[i] = "use_skill(\"" + s + "\")"
		}
		label := "Related skill: "
		if len(slugs) > 1 {
			label = "Related skills: "
		}
		parts = append(parts, label+strings.Join(slugs, ", ")+". Consider "+strings.Join(calls, " / ")+" to load instructions before proceeding.")
	}

	if v := stringTag(toneRE, raw); v != "" {
		parts = append(parts, "Tone: "+v+".")
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// splitSkillSlugs splits a comma-separated <skills> body into at most 3 slugs.
func splitSkillSlugs(body string) []string {
	if body == "" {
		return nil
	}
	var slugs []string
	for _, s := range strings.FieldsFunc(body, func(r rune) bool {
		return r == ',' || r == '，' || r == '、'
	}) {
		if s = strings.TrimSpace(s); s != "" {
			slugs = append(slugs, s)
		}
		if len(slugs) == 3 {
			break
		}
	}
	return slugs
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
