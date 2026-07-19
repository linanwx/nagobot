package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModel_NoBackendConfigured(t *testing.T) {
	c := NewChain(func() *Backend { return nil })
	if _, ok := c.Model(context.Background()); ok {
		t.Fatal("nil backend must report unavailable")
	}
	if _, err := c.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("Embed without a backend must error")
	}
}

func TestModel_IdentityIncludesProvider(t *testing.T) {
	c := NewChain(func() *Backend {
		return &Backend{Name: "siliconflow-cn", Model: "Qwen/Qwen3-Embedding-4B"}
	})
	id, ok := c.Model(context.Background())
	if !ok || id != "siliconflow-cn/Qwen/Qwen3-Embedding-4B" {
		t.Fatalf("got %q ok=%v", id, ok)
	}
}

// TestEmbed_SortsByIndex pins the order contract: the OpenAI-compatible spec
// carries an index per entry and arrival order is not guaranteed. Anchor
// slicing downstream (pos = vecs[:len(posAnchors)]) silently misclassifies if
// order is wrong, so this is load-bearing, not defensive.
func TestEmbed_SortsByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth header = %q", got)
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "m" || len(req.Input) != 2 {
			t.Errorf("request = %+v", req)
		}
		// Deliberately out of order.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float64{2, 2}},
				{"index": 0, "embedding": []float64{1, 1}},
			},
		})
	}))
	defer srv.Close()

	c := NewChain(func() *Backend {
		return &Backend{Name: "test", URL: srv.URL, APIKey: "k", Model: "m"}
	})
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Fatalf("order not restored by index: %v", vecs)
	}
}

func TestEmbed_CountMismatchAndHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": []float64{1}}},
		})
	}))
	defer srv.Close()
	c := NewChain(func() *Backend { return &Backend{Name: "t", URL: srv.URL, Model: "m"} })
	if _, err := c.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("vector-count mismatch must error, not truncate")
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer bad.Close()
	c = NewChain(func() *Backend { return &Backend{Name: "t", URL: bad.URL, Model: "m"} })
	if _, err := c.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("non-200 must error")
	}
}
