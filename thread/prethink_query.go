package thread

import (
	"context"
	"sync"
)

// One message, one embedding.
//
// Until 2026-07-27 each of the four embedding classifiers issued its own
// /embeddings request for the same user message, and three of those requests
// were byte-identical: destructive, search and coder all declared the same
// instruction ("retrieve reference messages that request the same kind of
// action") and embedded the same text. Three remote calls, three identical
// vectors. Only skills asked something genuinely different, and only in its
// instruction prefix.
//
// That was not a small waste, because the budget sees the MAXIMUM of the calls
// it waits for. Measured against OpenRouter's only upstream for this model
// (DeepInfra, verified as the single endpoint), one call is p50 496ms with a
// heavy right tail — P(>3s) around 3%. Four independent draws turn that into
// 1-0.97^4 ≈ 11.5%, and the observed budget-blow rate on the deployment was
// 12% (7/58). The tail was not the problem; drawing from it four times was.
//
// So the whole analysis now embeds ONE query vector and every classifier scores
// it locally against its own anchors. Measured on the same host: p90 2502ms →
// 911ms, max 6190ms → 1312ms, 0/15 over the budget.
//
// The cost of collapsing to one vector is that skills gave up its own
// instruction. That was measured before it was done, on the real skill pool and
// the hand set: at the operating margin (0.05) recall stays 31/31 and all 8
// negatives are still rejected, with slightly FEWER slugs returned on average
// (2.05 vs 2.23). The plateau runs 0.03–0.08 either way.

// preThinkQueryTask is the single instruction every classifier's query is
// embedded under. The anchor-side constants alias it rather than repeat it, so
// the three classifiers that instruct both sides cannot drift apart again.
const preThinkQueryTask = "Given a user message to an AI assistant, retrieve reference messages that request the same kind of action"

// preThinkQueryMaxRunes truncates long pastes before embedding: the request
// intent lives in the head of a message, and embedding models truncate
// internally anyway.
//
// 600 rather than the 800 search and skills used to apply, because one vector
// admits one truncation and 600 is the length destructive and coder were
// calibrated at. The two that give ground are the two whose failure direction is
// harmless — a missed web search, a missed skill hint — while destructive's is
// "an irreversible action proceeds unconfirmed".
const preThinkQueryMaxRunes = 600

// preThinkQuery builds the exact text every classifier embeds. It is the only
// place the instruction and the truncation are applied.
func preThinkQuery(model, msg string) string {
	if r := []rune(msg); len(r) > preThinkQueryMaxRunes {
		msg = string(r[:preThinkQueryMaxRunes])
	}
	return qwen3Instructed(model, preThinkQueryTask, msg)
}

// embedFn is the one operation a queryEmbedder needs, injectable so the
// round-trip count is testable without a backend.
type embedFn func(context.Context, []string) ([][]float64, error)

// preThinkEmbedFn is indirected so a test can count the round trips a whole
// analysis makes. The "one request per message" property is the entire point of
// this file and is worth an assertion rather than a comment.
var preThinkEmbedFn = func() embedFn { return searchEmbed.client.Embed }

// queryEmbedder is a per-message vector cache. Its lifetime is one call to
// localPreThink — the key is the query text, so there is nothing to invalidate
// and nothing to carry between messages.
type queryEmbedder struct {
	embed embedFn

	mu     sync.Mutex
	vecs   map[string][]float64
	failed map[string]error
	calls  int
}

func newQueryEmbedder(embed embedFn) *queryEmbedder {
	return &queryEmbedder{embed: embed, vecs: map[string][]float64{}, failed: map[string]error{}}
}

// prefetch embeds every distinct text in ONE round trip. A duplicate in texts
// costs nothing: the classifiers pass the same string precisely because they
// are asking the same question.
func (e *queryEmbedder) prefetch(ctx context.Context, texts ...string) error {
	want := make([]string, 0, len(texts))
	seen := map[string]bool{}
	e.mu.Lock()
	for _, t := range texts {
		if t == "" || seen[t] || e.vecs[t] != nil {
			continue
		}
		seen[t] = true
		want = append(want, t)
	}
	e.mu.Unlock()
	if len(want) == 0 {
		return nil
	}

	vecs, err := e.call(ctx, want)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		// Remember the failure rather than letting each classifier retry it.
		// Otherwise one dead round trip becomes four, against a backend that is
		// already struggling — the exact pile-up this rewrite exists to remove.
		for _, t := range want {
			e.failed[t] = err
		}
		return err
	}
	for i, t := range want {
		e.vecs[t] = vecs[i]
	}
	return nil
}

// vec returns the normalized vector for text, embedding on demand when it was
// not prefetched. On-demand is a correctness fallback, not a path anything
// should take in production: TestLocalPreThinkMakesOneEmbeddingRoundTrip pins
// that a normal message never reaches it.
func (e *queryEmbedder) vec(ctx context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	if v := e.vecs[text]; v != nil {
		e.mu.Unlock()
		return v, nil
	}
	if err := e.failed[text]; err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()

	vecs, err := e.call(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vecs[text] = vecs[0]
	return vecs[0], nil
}

// call embeds and normalizes. Vectors are normalized once here, so every
// scorer's dot product is a cosine and no call site normalizes again.
func (e *queryEmbedder) call(ctx context.Context, texts []string) ([][]float64, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()

	vecs, err := e.embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, errShortEmbedding{got: len(vecs), want: len(texts)}
	}
	for i := range vecs {
		normalize(vecs[i])
	}
	return vecs, nil
}

// roundTrips reports how many times the backend was called. Tests only.
func (e *queryEmbedder) roundTrips() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type errShortEmbedding struct{ got, want int }

func (e errShortEmbedding) Error() string {
	return "embedding: got " + itoa(e.got) + " vectors for " + itoa(e.want) + " inputs"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

type queryEmbedderKey struct{}

func withQueryEmbedder(ctx context.Context, e *queryEmbedder) context.Context {
	return context.WithValue(ctx, queryEmbedderKey{}, e)
}

func queryEmbedderFrom(ctx context.Context) *queryEmbedder {
	e, _ := ctx.Value(queryEmbedderKey{}).(*queryEmbedder)
	return e
}

// queryVector is what every classifier calls instead of embedding for itself.
//
// Riding on the context rather than on the classifier signatures is deliberate:
// the four classifiers are also called directly by their own tests and by
// WarmLocalPreThink, where no shared embedder exists and embedding on demand is
// the right behaviour. A parameter would have forced every one of those callers
// to invent one.
func queryVector(ctx context.Context, embed embedFn, text string) ([]float64, error) {
	if e := queryEmbedderFrom(ctx); e != nil {
		return e.vec(ctx, text)
	}
	return newQueryEmbedder(embed).vec(ctx, text)
}
