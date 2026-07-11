package thread

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/linanwx/nagobot/logger"
)

// The <skills> field is a retrieval problem wearing a generation costume: the
// pre-think agent is handed the whole skill list and asked to pick 0-3 slugs.
// A skill's description is written precisely to say when to use it, so matching
// a message against descriptions in embedding space does the same job without a
// model call — and cannot hallucinate a slug that does not exist, which the
// prompt currently has to beg the LLM not to do.
//
// Two properties the LLM path does not have, and we get for free:
//
//   - Cron-only skills can never be selected. dream / heartbeat-wake /
//     session-reflect / the *-dispatcher and *-updater skills all say "never
//     call directly" in their own description, so the filter reads that marker
//     rather than hardcoding a blocklist that would rot.
//   - "No skill fits" is a real answer. Below the similarity floor we return
//     nothing, which is what the prompt asks for ("do NOT pad to 3") and what an
//     eager LLM tends not to do.

// skillCandidate is one entry of the pool the pre-think prompt renders as
// {{SKILLS}} — the slug, the description that says when to use it, and any
// examples the skill ships.
//
// The description is the only routing channel, by design. A `tags` field used to
// exist and fed retrieval alone: BuildPromptSection never emitted it, so the LLM
// could not see it. Retrieval got a signal the router did not, and the two drifted
// apart — which is exactly the bug we were paying for. Whatever a skill wants to
// be found by now goes in the description, where both readers see it.
type skillCandidate struct {
	Slug        string
	Description string
	Examples    []string
}

// facets returns every text that should represent this skill in embedding space.
// A skill is matched by the BEST of them (max-pool), not by an average: a
// description and a concrete example are different views of the same capability,
// and averaging them would blur both.
func (c skillCandidate) facets() []string {
	out := []string{c.Slug + ": " + c.Description}
	for _, ex := range c.Examples {
		if strings.TrimSpace(ex) != "" {
			out = append(out, ex)
		}
	}
	return out
}

// schedulerOnlyMarkers identify skills driven by the scheduler rather than by a
// user. They are excluded from retrieval entirely — a user message must never be
// able to trigger dream or a *-dispatcher.
//
// Two markers, because the skills say it two different ways: dream /
// heartbeat-wake / session-reflect end with "never call directly", while the
// *-dispatcher and *-updater skills say "Used by the <name> cron task". Reading
// both beats a hardcoded slug blocklist that would rot the moment a skill is
// added — and it is not cosmetic: before this filter, the top match for "每天早上
// 八点提醒我喝水" was people-knowledge-updater, not manage-cron. The cron skills
// describe the same domains as the user-facing ones and crowd them out.
var schedulerOnlyMarkers = []string{
	"never call directly",
	"cron task",
}

func neverCallDirectly(desc string) bool {
	low := strings.ToLower(desc)
	for _, m := range schedulerOnlyMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// skillNoneAnchors describe the requests that no skill should handle. They are
// embedded alongside the skills and act as a rival class: a skill is offered
// only when it beats the best "none" anchor.
//
// This replaces the obvious design — an absolute cosine floor — which measurement
// showed cannot work. Cosine magnitude drifts with the query, and because every
// skill description is in English, a Chinese or Japanese message scores
// systematically lower against all of them: "每天早上八点提醒我喝水" matched
// manage-cron at 0.36 while its English twin matched at 0.57. Any fixed floor
// either rejects everything non-English or accepts everything. A z-score over the
// pool fails too (positives bottomed out at z=1.3, negatives peaked at z=2.4 —
// full overlap), because an irrelevant message still has *some* nearest skill.
// Only a rival class answers the actual question: is this closer to a skill, or
// to the kind of message that needs none?
var skillNoneAnchors = []string{
	"casual greeting, small talk, or thanking the assistant",
	"a general knowledge question the assistant answers from what it already knows",
	"translate, rewrite, summarize or proofread text the user pasted",
	"write code, debug a program, or explain a programming concept",
	"creative writing such as a poem, a story, an essay, or a joke",
	"solve a math problem or perform a calculation",
	"ask the assistant for advice, an opinion, or a recommendation in conversation",
	"an ordinary factual question with no tool or capability involved",
}

// qwen3Instruct is the query-side prefix Qwen3-Embedding is trained with for
// asymmetric retrieval. It is worth real points, especially cross-lingually
// (the drink-water reminder above goes from rank 2 to rank 1, and the Japanese
// image request from 0.48 to 0.72). Other models get the bare query — a prefix
// they were not trained on is just noise.
const qwen3Instruct = "Instruct: Given a user request to an AI assistant, retrieve the skill whose description says it should handle this request.\nQuery: "

func skillQueryText(model, msg string) string {
	if strings.Contains(model, "qwen3-embedding") {
		return qwen3Instruct + msg
	}
	return msg
}

const (
	maxRelatedSkills = 3
	// skillNoneMargin is how far a skill must beat the best "none" anchor to be
	// offered. Swept on the hand set against the current descriptions: +0.05 is
	// the frontier point (recall@3 30/31, all 8 negatives rejected, 1.69 slugs
	// returned on average — it does not pad to 3, which is what the pre-think
	// prompt asks for and an eager LLM does anyway). Lower margins buy the last
	// positive back at the cost of firing on greetings.
	skillNoneMargin = 0.05

	skillIndexTimeout = 60 * time.Second
	skillQueryTimeout = 5 * time.Second
	skillRetryAfter   = time.Minute
)

var skillIndex = &skillIndexState{}

type skillIndexState struct {
	mu       sync.Mutex
	key      string // fingerprint of (model, candidate pool)
	model    string
	slugs    []string
	groups   [][][]float64 // one group of facet vectors per skill
	noneVecs [][]float64
	lastTry  time.Time
}

// fingerprint identifies a (model, pool) pair so the index rebuilds when a
// skill is installed, edited, or removed — skills are hot-reloaded every turn.
func fingerprint(model string, cands []skillCandidate) string {
	h := sha256.New()
	h.Write([]byte(model))
	for _, c := range cands {
		h.Write([]byte{0})
		h.Write([]byte(c.Slug))
		for _, f := range c.facets() {
			h.Write([]byte{1})
			h.Write([]byte(f))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ensure builds (or reuses) the embedded index. Caller must hold s.mu.
func (s *skillIndexState) ensure(ctx context.Context, cands []skillCandidate) bool {
	model, ok := searchEmbed.client.Model(ctx)
	if !ok {
		return false // no local Ollama — caller falls back
	}

	usable := make([]skillCandidate, 0, len(cands))
	for _, c := range cands {
		if c.Slug == "" || c.Description == "" || neverCallDirectly(c.Description) {
			continue
		}
		usable = append(usable, c)
	}
	if len(usable) == 0 {
		return false
	}

	key := fingerprint(model, usable)
	if key == s.key && s.groups != nil {
		return true
	}
	if time.Since(s.lastTry) < skillRetryAfter {
		return false
	}
	s.lastTry = time.Now()

	// The slug carries real signal ("manage-cron", "send-image"), so it is
	// embedded alongside each facet rather than used only as a label.
	slugs := make([]string, len(usable))
	counts := make([]int, len(usable))
	var texts []string
	for i, c := range usable {
		f := c.facets()
		slugs[i] = c.Slug
		counts[i] = len(f)
		texts = append(texts, f...)
	}
	nSkillTexts := len(texts)
	texts = append(texts, skillNoneAnchors...)

	idxCtx, cancel := context.WithTimeout(ctx, skillIndexTimeout)
	defer cancel()
	vecs, err := searchEmbed.client.Embed(idxCtx, texts)
	if err != nil {
		logger.Warn("pre-think skill index: embedding failed", "model", model, "texts", len(texts), "err", err)
		return false
	}
	for i := range vecs {
		normalize(vecs[i])
	}

	groups := make([][][]float64, len(usable))
	off := 0
	for i, n := range counts {
		groups[i] = vecs[off : off+n]
		off += n
	}
	s.key, s.model, s.slugs = key, model, slugs
	s.groups, s.noneVecs = groups, vecs[nSkillTexts:]
	logger.Info("pre-think skill index ready", "model", model,
		"skills", len(slugs), "facets", nSkillTexts, "noneAnchors", len(s.noneVecs))
	return true
}

type scoredSkill struct {
	slug string
	sim  float64
}

// rankSkills scores every eligible skill against the message, best first, and
// returns the bar it has to clear: the best "none" anchor plus the margin.
// ok=false means the classifier is unavailable (no local Ollama).
func rankSkills(msg string, cands []skillCandidate) (ranked []scoredSkill, bar float64, ok bool) {
	if strings.TrimSpace(msg) == "" || len(cands) == 0 {
		return nil, 0, false
	}

	skillIndex.mu.Lock()
	defer skillIndex.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), skillIndexTimeout)
	defer cancel()
	if !skillIndex.ensure(ctx, cands) {
		return nil, 0, false
	}

	if r := []rune(msg); len(r) > searchEmbedMaxRunes {
		msg = string(r[:searchEmbedMaxRunes])
	}
	qCtx, qCancel := context.WithTimeout(context.Background(), skillQueryTimeout)
	defer qCancel()
	qs, err := searchEmbed.client.Embed(qCtx, []string{skillQueryText(skillIndex.model, msg)})
	if err != nil {
		logger.Warn("pre-think skill index: query embedding failed", "err", err)
		return nil, 0, false
	}
	q := qs[0]
	normalize(q)

	ranked = make([]scoredSkill, 0, len(skillIndex.slugs))
	for i, group := range skillIndex.groups {
		best := math.Inf(-1)
		for _, v := range group {
			if d := dot(q, v); d > best {
				best = d
			}
		}
		ranked = append(ranked, scoredSkill{skillIndex.slugs[i], best})
	}
	sort.Slice(ranked, func(a, b int) bool { return ranked[a].sim > ranked[b].sim })

	bestNone := math.Inf(-1)
	for _, n := range skillIndex.noneVecs {
		if d := dot(q, n); d > bestNone {
			bestNone = d
		}
	}
	return ranked, bestNone + skillNoneMargin, true
}

// relatedSkillsEmbed returns up to 3 slugs whose descriptions match the message
// better than the "none" class does. ok=false means the classifier is
// unavailable and the caller should fall back; an empty slice with ok=true is
// the real answer "no skill fits" — which the prompt asks for and an eager LLM
// tends not to give.
func relatedSkillsEmbed(msg string, cands []skillCandidate) (slugs []string, ok bool) {
	ranked, bar, ok := rankSkills(msg, cands)
	if !ok {
		return nil, false
	}
	for _, r := range ranked {
		if len(slugs) == maxRelatedSkills || r.sim <= bar {
			break
		}
		slugs = append(slugs, r.slug)
	}
	return slugs, true
}

func dot(a, b []float64) float64 {
	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}
