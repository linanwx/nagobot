package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// clientWithTags returns a Client pointed at a fake Ollama that reports exactly
// these tags, in exactly this order.
func clientWithTags(t *testing.T, tags ...string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		type model struct {
			Name string `json:"name"`
		}
		body := struct {
			Models []model `json:"models"`
		}{}
		for _, tag := range tags {
			body.Models = append(body.Models, model{Name: tag})
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	c := NewLocal()
	c.baseURL = srv.URL
	return c
}

// The size of the model actually matters here in a way it usually does not:
// pre-think budgets 2s for three concurrent embeddings, and the 0.6B is the only
// qwen3-embedding tag that fits. Picking the 8B because it happened to come back
// first from /api/tags would blow the budget on every message and degrade the
// destructive classifier to its regex fallback — silently.
func TestModel_PinsQwen06b(t *testing.T) {
	for _, order := range [][]string{
		{"qwen3-embedding:8b", "qwen3-embedding:0.6b", "qwen3-embedding:4b"},
		{"qwen3-embedding:0.6b", "qwen3-embedding:8b"},
		{"qwen3-embedding:4b", "qwen3-embedding:8b", "qwen3-embedding:0.6b"},
	} {
		got, ok := clientWithTags(t, order...).Model(context.Background())
		if !ok || got != "qwen3-embedding:0.6b" {
			t.Errorf("tags %v → %q (ok=%v), want qwen3-embedding:0.6b", order, got, ok)
		}
	}
}

// Without the pinned tag installed the family entry still matches, so a host that
// only pulled the 4B keeps a working classifier — just a slower one.
func TestModel_FallsBackWithinFamily(t *testing.T) {
	got, ok := clientWithTags(t, "qwen3-embedding:4b").Model(context.Background())
	if !ok || got != "qwen3-embedding:4b" {
		t.Errorf("got %q (ok=%v), want qwen3-embedding:4b", got, ok)
	}
}

// Same installation, different /api/tags order, same answer. The response order
// is not contractual, and a model that changes across restarts changes every
// threshold in thread/prethink_*.go with it.
func TestModel_FamilyFallbackIsDeterministic(t *testing.T) {
	a, _ := clientWithTags(t, "bge-m3:latest", "bge-m3:567m").Model(context.Background())
	b, _ := clientWithTags(t, "bge-m3:567m", "bge-m3:latest").Model(context.Background())
	if a != b {
		t.Errorf("response order changed the verdict: %q vs %q", a, b)
	}
}

func TestModel_PreferenceOrderWins(t *testing.T) {
	// nomic is installed and listed first; qwen still wins — it is multilingual
	// and nomic is not, which is the whole reason for the ranking.
	got, ok := clientWithTags(t, "nomic-embed-text:latest", "qwen3-embedding:0.6b").Model(context.Background())
	if !ok || got != "qwen3-embedding:0.6b" {
		t.Errorf("got %q (ok=%v), want qwen3-embedding:0.6b", got, ok)
	}
}

func TestModel_NoUsableModel(t *testing.T) {
	// A running Ollama with only chat models is the same as no Ollama, for us.
	if got, ok := clientWithTags(t, "llama3:8b", "qwen3:14b").Model(context.Background()); ok {
		t.Errorf("got %q, want no usable embedding model", got)
	}
}
