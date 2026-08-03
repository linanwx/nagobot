package thread

import (
	"context"
	"strings"
	"time"

	"github.com/linanwx/nagobot/logger"
	"github.com/linanwx/nagobot/obs"
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
//
// <coder> is a sixth field with no LLM ancestor: added later (2026-07) to route
// code-production requests toward the coder subagent, which is bound to a
// code-specialized model. Same architecture as <search>: regex fallback plus an
// embedding prototype classifier, run under the shared budget.
const (
	// preThinkChatEntries is how much recent conversation <destructive> gets to see.
	// It needs it: "执行吧" carries no danger in itself, only in the turn it answers.
	preThinkChatEntries = 16

	// preThinkBudget caps the whole local analysis. The embedding classifiers talk
	// to the remote backend and normally answer well inside the budget, but a
	// slow or wedged endpoint must never become the user's latency: whatever has not
	// answered by the deadline falls back to its regex verdict and the turn goes on.
	//
	// The four classifiers run CONCURRENTLY, which is what makes one budget enough.
	// Run serially their individual timeouts would add up to twenty seconds — worse
	// than the LLM call this replaces.
	//
	// 3s, not the 2s the local-Ollama era used: the budget must cover the p90 of
	// one remote round-trip from the WORST deployed host, or the semantic layer
	// silently degrades to regex exactly where it is needed. Measured warm p50 to
	// SiliconFlow: ~170ms from a CN VPS, but ~1.5s from a Mac whose route to .cn
	// goes the long way — at a 2s budget that host lost the destructive
	// classifier on a coin flip.
	preThinkBudget = 3 * time.Second
)

// preThinkAction computes the action hint for a user message. No model call.
//
// Returns "" when nothing fires, which is the common case and is correct: most
// messages need no special handling, and the old prompt's habit of always saying
// SOMETHING (a tone, a padded skill list) was noise the main model had to read.
func preThinkAction(ctx context.Context, t *Thread, userMsg string) string {
	userMsg = strings.TrimSpace(userMsg)
	if userMsg == "" {
		return ""
	}
	recentChat := session.ReadRecentChat(t.mgr.SessionDir(t.sessionKey), preThinkChatEntries, t.location())

	hint, took := localPreThink(ctx, userMsg, recentChat, t.skillCandidates())
	logger.Info("pre-think (local)",
		"sessionKey", t.sessionKey,
		"took", took,
		"hintLen", len(hint),
	)
	return hint
}

// localPreThink is preThinkAction without the Thread — the whole analysis, taking
// only what it reads. Split out so it can be exercised against a real backend and
// the real skill pool in a test, which is the only place the concurrency, the
// budget, and the embedding round-trip are actually load-bearing.
// traceCtx is the SPAN PARENT only. The budget context below is deliberately
// still derived from context.Background(), not from it: cancelling the four
// classifiers when the turn's context dies is a behaviour change this
// instrumentation has no business making.
func localPreThink(traceCtx context.Context, userMsg, recentChat string, cands []skillCandidate) (string, time.Duration) {
	start := time.Now()

	traceCtx, span := obs.Start(traceCtx, "prethink",
		obs.Int("budget_ms", int(preThinkBudget.Milliseconds())),
		obs.Len("msg", userMsg),
		obs.Int("skill_candidates", len(cands)),
	)
	defer span.End()

	// The regex-only verdicts are computed first and stand as the fallback. They
	// cost microseconds and touch nothing, so a blown budget degrades to a weaker
	// answer rather than to no answer — which matters most for <destructive>, where
	// reporting false on a timeout would let an irreversible action through
	// unconfirmed.
	destructive := isDestructiveRegex(userMsg, recentChat)
	search := needsSearchRegex(userMsg)
	coder := needsCoderRegex(userMsg)
	var slugs []string // no regex fallback exists; skills is embedding-only

	// One context for the four classifiers, cancelled when the budget expires OR
	// as soon as all four have answered. Without it the goroutines below would
	// outlive the turn they were started for: each classifier's own timeouts are
	// five seconds, so against a slow backend we would give up here at two seconds,
	// answer from the regexes, and leave four HTTP requests running for three more
	// — and then do it again on the next message, and the one after that, until the
	// classifier mutexes are a queue of embeddings nobody will ever read.
	//
	// Index CONSTRUCTION is deliberately not on this context; see the comment on
	// searchEmbedState.ensure. A cold build legitimately takes longer than the whole
	// budget, and cancelling it every time would mean it never finishes at all.
	ctx, cancel := context.WithTimeout(context.Background(), preThinkBudget)
	defer cancel()

	// One embedding round trip for the whole analysis. The four classifiers ask
	// the same question of the same text (see prethink_query.go), so they share
	// one vector and score it locally against their own anchors. Prefetching
	// here rather than letting each classifier embed for itself is what turns
	// four independent draws from a heavy-tailed backend into one.
	//
	// A failure is not handled here: the classifiers below simply report
	// themselves unavailable and the regex verdicts stand, which is the same
	// degradation path a timeout takes.
	embedder := newQueryEmbedder(preThinkEmbedFn())
	ctx = withQueryEmbedder(ctx, embedder)
	if model, ok := searchEmbed.client.Model(ctx); ok {
		texts := []string{preThinkQuery(model, userMsg)}
		if subject := destructiveSubject(userMsg, recentChat); subject != userMsg {
			texts = append(texts, preThinkQuery(model, subject))
		}
		// The span is parented to traceCtx, but the CALL runs on the budget
		// context. Those are two different contexts on purpose and the
		// distinction is load-bearing: traceCtx is the turn's, and it carries no
		// deadline, so prefetching on it puts an unbounded remote call on the
		// user's critical path ahead of every classifier. Observed in
		// production before this was separated: prethink spans of 10.8s and
		// 31.9s against a 3s budget.
		_, espan := obs.Start(traceCtx, "prethink.embed", obs.Int("inputs", len(texts)))
		if err := embedder.prefetch(ctx, texts...); err != nil {
			espan.Fail(err)
		}
		espan.End()
	}

	type result struct {
		field string
		flag  bool
		slugs []string
	}
	results := make(chan result, 4)
	// Each classifier gets its own span so the one that eats the budget is
	// named. They can outlive this function — the budget path below stops
	// waiting without stopping them — in which case a classify span simply ends
	// after its parent, which is exactly the shape that identifies the culprit.
	classify := func(field string, fn func(context.Context) result) {
		go func() {
			cctx, s := obs.Start(traceCtx, "prethink.classify", obs.Str("field", field))
			r := fn(cctx)
			s.Set(obs.Bool("flag", r.flag), obs.Int("skills", len(r.slugs)))
			s.End()
			results <- r
		}()
	}
	classify("destructive", func(c context.Context) result {
		return result{field: "destructive", flag: isDestructive(ctx, userMsg, recentChat)}
	})
	classify("search", func(c context.Context) result {
		return result{field: "search", flag: needsSearch(ctx, userMsg)}
	})
	classify("coder", func(c context.Context) result {
		return result{field: "coder", flag: needsCoder(ctx, userMsg)}
	})
	classify("skills", func(c context.Context) result {
		s, _ := relatedSkillsEmbed(ctx, userMsg, cands)
		return result{field: "skills", slugs: s}
	})

	for pending := 4; pending > 0; {
		select {
		case r := <-results:
			pending--
			switch r.field {
			case "destructive":
				destructive = r.flag
			case "search":
				search = r.flag
			case "coder":
				coder = r.flag
			case "skills":
				slugs = r.slugs
			}
		case <-ctx.Done():
			logger.Warn("pre-think: local analysis exceeded budget, falling back to regex verdicts",
				"budget", preThinkBudget, "stillRunning", pending)
			pending = 0
		}
	}

	hint := composePreThinkHint(destructive, search, coder,
		isIncludeInvestigator(userMsg), hasWebURL(userMsg), slugs)

	logger.Debug("pre-think signals",
		"destructive", destructive,
		"search", search,
		"coder", coder,
		"skills", slugs,
	)
	return hint, time.Since(start)
}

// composePreThinkHint renders the surviving signals into the action hint. Every
// line states what to DO, and nothing else.
//
// The wording used to be carried over verbatim from the old XML-parsing path,
// which meant each line also restated the classifier's own decision criteria:
// <destructive> spent 130 of its 249 characters defining what "destructive"
// means, <coder> 88 of 227, <search> 128 of 179. That text is written for a
// reader who has to MAKE the call. The main model does not make it and cannot
// overturn it — the classifier already ran — so the criteria were freight.
// <search> also carried a ">10%" threshold that was an instruction to the OLD
// LLM classifier; once the classifier became an embedding prototype the number
// corresponded to nothing, and the model reading it had no way to act on it.
//
// The labels ("Destructive action:", "Search:", …) are deliberately unchanged.
// The label is the signal; it is what the tests pin and what the deployment's
// sessions can be grepped for. Worst case (all six firing) went 1074 → ~460
// characters, and the hint fires on more user messages than the per-field rates
// suggest — 17 of 30 in one sampled live session, mostly <search> and <skills>.
func composePreThinkHint(destructive, search, coder, investigator, webURL bool, slugs []string) string {
	var parts []string

	if destructive {
		parts = append(parts, "Destructive action: confirm with the user before executing (just ask in plain text), and prefer a dry-run or reversible path.")
	}
	if search {
		parts = append(parts, "Search: a relevant fact may have changed since training. Consider dispatching a search subagent.")
	}
	if coder {
		parts = append(parts, "Code task: consider dispatching the coder subagent.")
	}
	if investigator {
		parts = append(parts, "Investigator: dispatch an investigator subagent before responding.")
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
		// The slugs used to be listed twice — bare, then again inside the calls.
		// The call form is the actionable one, so the bare list is gone.
		parts = append(parts, label+"consider "+strings.Join(calls, " / ")+" first.")
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

// WarmLocalPreThink builds the four embedding indexes ahead of the first user
// message, in the background.
//
// Most of what it used to do is now free. The static anchor sets ship as
// pre-computed vectors (embedding/anchors.bin), so destructive, search, coder
// and the skill "none" anchors resolve from memory with no request at all —
// which is what removed the failure this warm-up could never work around: on a
// backend whose route rejects an 85-input request, those indexes did not
// eventually build, they never built.
//
// What remains is the skill descriptions. The shipped ones are baked too, so on
// a stock deployment this still touches no network; a workspace with added or
// hand-edited skills embeds exactly those, which is a handful of texts. Doing it
// here rather than on the first message keeps that cost off the turn a user is
// most likely to be watching. Warming costs nothing on a machine without a
// configured backend: resolution fails fast and every classifier simply reports
// itself unavailable.
func WarmLocalPreThink(reg *skills.Registry) {
	go func() {
		start := time.Now()
		if !warmPreThinkIndexes(skillCandidatesFrom(reg)) {
			logger.Warn("pre-think: no embedding backend configured, running on regex only",
				"note", "<destructive> loses its semantic layer without one; configure a siliconflow or openrouter key")
			return
		}
		logger.Info("pre-think: local indexes warm", "took", time.Since(start).Round(time.Millisecond))
	}()
}

// warmPreThinkIndexes builds the four indexes, one at a time, with no deadline.
//
// It deliberately does NOT go through localPreThink. That path runs the classifiers
// concurrently under preThinkBudget, and a cold build of all of them takes about two
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
	_, _ = classifyCoderEmbed(ctx, "warm")
	return true
}
