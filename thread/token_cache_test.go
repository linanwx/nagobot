package thread

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/linanwx/nagobot/provider"
)

// tokenCacheFixtures spans every branch of estimateMessageTokensUncached, so a
// field sweep over them exercises each term of the estimate at least once.
// Reasoning is the reason there are several: ReasoningContent and
// ReasoningDetails are mutually exclusive in the estimator, and the trimmed
// flag silences both — one fixture cannot make all three observable.
func tokenCacheFixtures() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "帮我看一下这个 traces.jsonl 里各阶段耗时分布"},
		{Role: "assistant", Content: "let me check", ReasoningContent: "the user wants a breakdown by span name"},
		{Role: "assistant", Content: "checking", ReasoningDetails: json.RawMessage(`{"signature":"abcdefghijklmnop"}`)},
		{Role: "assistant", Content: "trimmed", ReasoningContent: "old thinking", ReasoningTrimmed: true},
		{
			Role:    "assistant",
			Content: "running a search",
			ToolCalls: []provider.ToolCall{
				{ID: "call_1", Type: "function", Function: provider.FunctionCall{Name: "web_search", Arguments: `{"query":"deepseek v4 pro"}`}},
				{ID: "call_2", Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/a.md"}`}},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "web_search", Content: strings.Repeat("result line\n", 40)},
		{Role: "user", Content: "look at this <<media:image/jpeg:/nonexistent/photo.jpg>> please"},
		{Role: "system", Content: ""},
	}
}

// TestCachedEstimateMatchesUncached is the correctness floor: memoization must
// be invisible in the numbers. Runs each fixture twice — the second pass is the
// one served from cache.
func TestCachedEstimateMatchesUncached(t *testing.T) {
	resetTokenCache()
	for i, m := range tokenCacheFixtures() {
		want := estimateMessageTokensUncached(m)
		for pass := 1; pass <= 2; pass++ {
			if got := EstimateMessageTokens(m); got != want {
				t.Errorf("fixture %d pass %d: cached=%d uncached=%d", i, pass, got, want)
			}
		}
	}
}

// TestTokenCacheKeyCoversEveryEstimatedField is the guard that survives future
// edits to provider.Message. It walks every field, mutates it, and asserts the
// safety property directly:
//
//	the estimate changed  =>  the key changed
//
// Deliberately NOT an allow-list of hashed fields. A list has to be maintained
// by whoever adds a field, which is exactly the person who already forgot; this
// formulation needs no maintenance and fails loudly the moment a new field
// starts moving the estimate without moving the key. Fields that cannot change
// the count (ID, Timestamp, Source, Media, …) are free to be absent from the
// key and this test says nothing about them.
func TestTokenCacheKeyCoversEveryEstimatedField(t *testing.T) {
	fixtures := tokenCacheFixtures()
	typ := reflect.TypeOf(provider.Message{})
	seen := map[string]bool{}

	for fi, base := range fixtures {
		for f := 0; f < typ.NumField(); f++ {
			field := typ.Field(f)
			mutated, ok := mutateField(base, f)
			if !ok {
				continue
			}
			seen[field.Name] = true

			beforeTokens := estimateMessageTokensUncached(base)
			afterTokens := estimateMessageTokensUncached(mutated)
			if beforeTokens == afterTokens {
				continue // field does not feed the estimate on this fixture
			}
			if tokenCacheKey(base) == tokenCacheKey(mutated) {
				t.Errorf("fixture %d: mutating %s changes the estimate (%d -> %d) but not the cache key — "+
					"cached lookups would return the wrong count; add it to tokenCacheKey",
					fi, field.Name, beforeTokens, afterTokens)
			}
		}
	}

	// A field the mutator cannot touch is a field this test silently skips.
	for f := 0; f < typ.NumField(); f++ {
		if name := typ.Field(f).Name; !seen[name] {
			t.Errorf("no mutation was generated for provider.Message.%s (%s) — extend mutateField, "+
				"otherwise this field is unguarded", name, typ.Field(f).Type)
		}
	}
}

// mutateField returns a copy of m with field i changed to a different value.
// Reports false when the field's type has no mutation rule.
func mutateField(m provider.Message, i int) (provider.Message, bool) {
	out := m
	v := reflect.ValueOf(&out).Elem().Field(i)
	switch v.Interface().(type) {
	case string:
		v.SetString(v.String() + "x")
	case bool:
		v.SetBool(!v.Bool())
	case int:
		v.SetInt(v.Int() + 1)
	case time.Time:
		v.Set(reflect.ValueOf(m.Timestamp.Add(time.Second)))
	case []string:
		v.Set(reflect.ValueOf(append(append([]string{}, m.Media...), "<<media:image/png:/x.png>>")))
	case json.RawMessage:
		v.Set(reflect.ValueOf(json.RawMessage(append(append([]byte{}, m.ReasoningDetails...), 'x'))))
	case []provider.ToolCall:
		extra := provider.ToolCall{ID: "call_added", Type: "function", Function: provider.FunctionCall{Name: "exec", Arguments: `{"cmd":"ls"}`}}
		v.Set(reflect.ValueOf(append(append([]provider.ToolCall{}, m.ToolCalls...), extra)))
	default:
		return out, false
	}
	return out, true
}

// TestTokenCacheKeyDistinguishesFieldBoundaries pins the length prefixing.
// Without it these two hash identically and one message returns the other's
// token count.
func TestTokenCacheKeyDistinguishesFieldBoundaries(t *testing.T) {
	a := provider.Message{Role: "ab", Content: ""}
	b := provider.Message{Role: "a", Content: "b"}
	if tokenCacheKey(a) == tokenCacheKey(b) {
		t.Fatal("field boundaries collide: length prefixing is not being applied")
	}
}

// TestTokenCacheSurvivesGenerationFlip drives more distinct messages than one
// generation holds and checks that every answer is still right — a flip must
// cost recomputation at worst, never a wrong number.
func TestTokenCacheSurvivesGenerationFlip(t *testing.T) {
	resetTokenCache()
	orig := tokenCacheGenSize
	tokenCacheGenSize = 64
	defer func() {
		tokenCacheGenSize = orig
		resetTokenCache()
	}()

	msgs := make([]provider.Message, 0, 400)
	for i := 0; i < 400; i++ {
		msgs = append(msgs, provider.Message{Role: "user", Content: fmt.Sprintf("message number %d with some 中文 padding", i)})
	}
	for _, m := range msgs {
		EstimateMessageTokens(m)
	}
	// Re-read in reverse: the head is now two generations old and gone.
	for i := len(msgs) - 1; i >= 0; i-- {
		if got, want := EstimateMessageTokens(msgs[i]), estimateMessageTokensUncached(msgs[i]); got != want {
			t.Fatalf("message %d after flip: cached=%d uncached=%d", i, got, want)
		}
	}

	tokenCacheMu.RLock()
	cur, old := len(tokenCacheCur), len(tokenCacheOld)
	tokenCacheMu.RUnlock()
	if cur > tokenCacheGenSize || old > tokenCacheGenSize {
		t.Fatalf("generation exceeded its cap: cur=%d old=%d cap=%d", cur, old, tokenCacheGenSize)
	}
}

// TestTokenCacheCollisionDiscriminator: a key hit whose content length differs
// must recompute rather than return the stored count.
func TestTokenCacheCollisionDiscriminator(t *testing.T) {
	resetTokenCache()
	key := tokenCacheKey(provider.Message{Role: "user", Content: "hello"})
	storeTokenCache(key, tokenCacheEntry{tokens: 9999, contentLen: 1})
	if got := EstimateMessageTokens(provider.Message{Role: "user", Content: "hello"}); got == 9999 {
		t.Fatal("a stored entry with a mismatched contentLen was trusted")
	}
}

// TestEstimateMessagesTokensUnchangedByCache guards the aggregate path both
// live turns and the web history read go through.
func TestEstimateMessagesTokensUnchangedByCache(t *testing.T) {
	msgs := tokenCacheFixtures()
	resetTokenCache()
	want := 3
	for _, m := range msgs {
		want += estimateMessageTokensUncached(m)
	}
	if got := EstimateMessagesTokens(msgs); got != want {
		t.Fatalf("cold: %d, want %d", got, want)
	}
	if got := EstimateMessagesTokens(msgs); got != want {
		t.Fatalf("warm: %d, want %d", got, want)
	}
}
