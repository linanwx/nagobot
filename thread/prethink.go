package thread

import (
	"context"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
)

// Pre-think used to be an LLM call: every user message blocked for up to ten
// seconds while a `fast` model read the request and emitted ten XML fields, which
// were parsed back into an action hint for the main model. It is now local
// arithmetic — regexes and a local embedding model — and the LLM call is gone.
//
// Five of the ten fields survived. The other five were dropped, and the reasons
// are worth keeping because they are the reasons this is a net win rather than a
// lossy shortcut:
//
//	tone                  83% constant, and copied from USER.md. Near-zero information.
//	is_multi_step         The LLM's verdict was, in effect, len(message) > 160. A rune
//	                      count reproduced it at 73.5% against a 66% do-nothing baseline;
//	                      an embedding classifier scored 58%, i.e. worse than a constant.
//	                      Whatever the field measured was free, so paying a model for it
//	                      was not worth it — and the hint it produced ("plan the steps")
//	                      is advice a competent main model follows anyway.
//	hallucination         Dropped by decision, not by measurement.
//	needs_verification    Dropped by decision, not by measurement.
//	confusing_terminology Dropped by decision, not by measurement.
//
// What is left costs single-digit milliseconds instead of seconds, and two of the
// five (has_web_url, and skills, which cannot hallucinate a slug that does not
// exist) are strictly more accurate than the model they replaced.
const (
	// preThinkChatEntries is how much recent conversation <destructive> gets to see.
	// It needs it: "执行吧" carries no danger in itself, only in the turn it answers.
	preThinkChatEntries = 16

	// preThinkBudget caps the whole local analysis. The embedding classifiers talk
	// to Ollama over localhost and normally answer in tens of milliseconds, but a
	// wedged or cold Ollama must never become the user's latency: whatever has not
	// answered by the deadline falls back to its regex verdict and the turn goes on.
	//
	// The three classifiers run CONCURRENTLY, which is what makes one budget enough.
	// Run serially their individual timeouts would add up to fifteen seconds — worse
	// than the LLM call this replaces.
	preThinkBudget = 2 * time.Second
)

// preThinkAction computes the action hint for a user message. No model call.
//
// Returns "" when nothing fires, which is the common case and is correct: most
// messages need no special handling, and the old prompt's habit of always saying
// SOMETHING (a tone, a padded skill list) was noise the main model had to read.
func preThinkAction(t *Thread, userMsg string) string {
	userMsg = strings.TrimSpace(userMsg)
	if userMsg == "" {
		return ""
	}
	recentChat := session.ReadRecentChat(t.mgr.SessionDir(t.sessionKey), preThinkChatEntries, t.location())

	hint, took := localPreThink(userMsg, recentChat, t.skillCandidates())
	logger.Info("pre-think (local)",
		"sessionKey", t.sessionKey,
		"took", took,
		"hintLen", len(hint),
	)
	return hint
}

// localPreThink is preThinkAction without the Thread — the whole analysis, taking
// only what it reads. Split out so it can be exercised against a real Ollama and
// the real skill pool in a test, which is the only place the concurrency, the
// budget, and the embedding round-trip are actually load-bearing.
func localPreThink(userMsg, recentChat string, cands []skillCandidate) (string, time.Duration) {
	start := time.Now()

	// The regex-only verdicts are computed first and stand as the fallback. They
	// cost microseconds and touch nothing, so a blown budget degrades to a weaker
	// answer rather than to no answer — which matters most for <destructive>, where
	// reporting false on a timeout would let an irreversible action through
	// unconfirmed.
	destructive := isDestructiveRegex(userMsg, recentChat)
	search := needsSearchRegex(userMsg)
	var slugs []string // no regex fallback exists; skills is embedding-only

	// One context for the three classifiers, cancelled when the budget expires OR
	// as soon as all three have answered. Without it the goroutines below would
	// outlive the turn they were started for: each classifier's own timeouts are
	// five seconds, so against a slow Ollama we would give up here at two seconds,
	// answer from the regexes, and leave three HTTP requests running for three more
	// — and then do it again on the next message, and the one after that, until the
	// classifier mutexes are a queue of embeddings nobody will ever read.
	//
	// Index CONSTRUCTION is deliberately not on this context; see the comment on
	// searchEmbedState.ensure. A cold build legitimately takes longer than the whole
	// budget, and cancelling it every time would mean it never finishes at all.
	ctx, cancel := context.WithTimeout(context.Background(), preThinkBudget)
	defer cancel()

	type result struct {
		field string
		flag  bool
		slugs []string
	}
	results := make(chan result, 3)
	go func() { results <- result{field: "destructive", flag: isDestructive(ctx, userMsg, recentChat)} }()
	go func() { results <- result{field: "search", flag: needsSearch(ctx, userMsg)} }()
	go func() {
		s, _ := relatedSkillsEmbed(ctx, userMsg, cands)
		results <- result{field: "skills", slugs: s}
	}()

	for pending := 3; pending > 0; {
		select {
		case r := <-results:
			pending--
			switch r.field {
			case "destructive":
				destructive = r.flag
			case "search":
				search = r.flag
			case "skills":
				slugs = r.slugs
			}
		case <-ctx.Done():
			logger.Warn("pre-think: local analysis exceeded budget, falling back to regex verdicts",
				"budget", preThinkBudget, "stillRunning", pending)
			pending = 0
		}
	}

	hint := composePreThinkHint(destructive, search,
		isIncludeInvestigator(userMsg), hasWebURL(userMsg), slugs)

	logger.Debug("pre-think signals",
		"destructive", destructive,
		"search", search,
		"skills", slugs,
	)
	return hint, time.Since(start)
}

// composePreThinkHint renders the surviving signals into the action hint. The
// wording is carried over verbatim from the old XML-parsing path: the main model's
// behaviour is tuned against these exact sentences, so changing the classifier
// underneath is a big enough change on its own.
func composePreThinkHint(destructive, search, investigator, webURL bool, slugs []string) string {
	var parts []string

	if destructive {
		parts = append(parts, "Destructive action: fulfilling this may delete data, send/publish to others, write outside the workspace, or trigger irreversible side effects. Confirm with the user via dispatch(to=user) before executing, and prefer a dry-run or reversible path.")
	}
	if search {
		parts = append(parts, "Search: there is a meaningful chance (>10%) a relevant fact has changed since the model's training cutoff or needs an authoritative source. Consider dispatching a search subagent.")
	}
	if investigator {
		parts = append(parts, "Investigator: you must call dispatch to fan out an investigator subagent before responding to the user.")
	}
	if webURL {
		parts = append(parts, "Web URL present: consider using playwright to open it.")
	}
	if len(slugs) > 0 {
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

	return strings.Join(parts, " ")
}

// skillCandidates snapshots the thread's live skill registry for retrieval. Skills
// are hot-reloaded, so this is read per turn rather than cached; the retrieval
// index downstream keys itself on a fingerprint of this list and rebuilds only
// when it actually changes.
func (t *Thread) skillCandidates() []skillCandidate {
	cfg := t.cfg()
	if cfg == nil {
		return nil
	}
	return skillCandidatesFrom(cfg.Skills)
}

func skillCandidatesFrom(reg *skills.Registry) []skillCandidate {
	if reg == nil {
		return nil
	}
	list := reg.List()
	cands := make([]skillCandidate, 0, len(list))
	for _, s := range list {
		if s.Slug == "" || s.Description == "" {
			continue
		}
		cands = append(cands, skillCandidate{
			Slug:        s.Slug,
			Description: s.Description,
			Examples:    s.Examples,
		})
	}
	return cands
}

// WarmLocalPreThink builds the three embedding indexes ahead of the first user
// message, in the background.
//
// Without it the first message of a fresh process pays ~1.5s to embed the anchor
// sets and the skill descriptions — inside the budget, but a visible stall on
// exactly the turn a user is most likely to be watching. Warming costs nothing on
// a machine without Ollama: the model probe fails fast and every classifier simply
// reports itself unavailable.
func WarmLocalPreThink(reg *skills.Registry) {
	go func() {
		start := time.Now()
		if !warmPreThinkIndexes(skillCandidatesFrom(reg)) {
			logger.Info("pre-think: no local embedding model, running on regex only",
				"note", "<destructive> loses its semantic layer without one")
			return
		}
		logger.Info("pre-think: local indexes warm", "took", time.Since(start).Round(time.Millisecond))
	}()
}

// warmPreThinkIndexes builds the three indexes, one at a time, with no deadline.
//
// It deliberately does NOT go through localPreThink. That path runs the classifiers
// concurrently under preThinkBudget, and a cold build of all three takes about two
// seconds — right at the budget, so the warm-up would time out on itself and then
// claim in the log that it had finished. The builds would still complete in their
// own goroutines, but the first real message would race them and pay the remainder.
// Warming is not on anyone's critical path; it can just take the time it takes.
//
// Returns false when there is no local embedding model, which is not an error.
func warmPreThinkIndexes(cands []skillCandidate) bool {
	ctx := context.Background()
	if _, ok := relatedSkillsEmbed(ctx, "warm", cands); !ok {
		return false
	}
	_, _ = classifySearchEmbed(ctx, "warm")
	_, _ = classifyDestructiveEmbed(ctx, "warm")
	return true
}
