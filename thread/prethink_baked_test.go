package thread

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/linanwx/nagobot/embedding"
	"github.com/linanwx/nagobot/skills"
)

// bakedModelProbe is any spelling of the pinned model that triggers the
// instruction format. qwen3Instructed branches on a substring, so the TEXT it
// produces is identical whichever provider prefix is in front — which is what
// makes one table serve the whole backend chain.
const bakedModelProbe = "Qwen/Qwen3-Embedding-4B"

// builtinSkillsDir is where the shipped skills live in the source tree. The
// generator reads them from here rather than from a workspace: these are the
// texts the binary is built with, and a deployment that has hand-edited its
// copy is supposed to MISS the table and embed its own version.
const builtinSkillsDir = "../cmd/templates/skills"

// bakedTexts enumerates every string the pre-think classifiers would send to
// the embeddings endpoint as an anchor, spelled exactly as they send it.
//
// Which side carries the instruction is per-classifier and measured (see
// qwen3Instructed): destructive and search instruct their anchors, coder and
// skill retrieval embed theirs raw. Getting one of those wrong here does not
// break anything loudly — it produces a table whose keys nobody ever looks up,
// so every anchor quietly falls back to the network. That is what
// TestBakedAnchorsCoverEveryStaticText exists to catch.
func bakedTexts(cands []skillCandidate) []string {
	var out []string

	for _, a := range append(append([]string{}, destructivePosAnchors...), destructiveNegAnchors...) {
		out = append(out, qwen3Instructed(bakedModelProbe, destructiveEmbedTask, a))
	}
	for _, a := range append(append([]string{}, searchPosAnchors...), searchNegAnchors...) {
		out = append(out, qwen3Instructed(bakedModelProbe, searchEmbedTask, a))
	}
	out = append(out, coderPosAnchors...)
	out = append(out, coderNegAnchors...)
	for _, c := range cands {
		out = append(out, c.facets()...)
	}
	out = append(out, skillNoneAnchors...)

	seen := make(map[string]bool, len(out))
	uniq := out[:0]
	for _, t := range out {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		uniq = append(uniq, t)
	}
	return uniq
}

func builtinSkillCandidates(t *testing.T) []skillCandidate {
	t.Helper()
	reg := skills.NewRegistry()
	if err := reg.LoadFromDirectory(builtinSkillsDir); err != nil {
		t.Fatalf("load builtin skills from %s: %v", builtinSkillsDir, err)
	}
	cands := skillCandidatesFrom(reg)
	if len(cands) == 0 {
		t.Fatalf("no builtin skills found in %s", builtinSkillsDir)
	}
	return cands
}

// TestBakedAnchorsCoverEveryStaticText is the drift guard, and it is the whole
// reason the generated file is safe to keep in the tree.
//
// The anchors live in Go source and their vectors live in a binary blob. Edit
// an anchor without regenerating and the blob still parses, still loads, still
// reports itself healthy — it just holds a vector for a sentence that no longer
// exists, while the real one goes to the network on every process start. On a
// deployment whose backend rejects that request (which is the entire reason
// this table exists) the classifier would silently go dark. So the coupling is
// asserted rather than remembered.
//
// It needs no network: it only asks whether each text is present.
func TestBakedAnchorsCoverEveryStaticText(t *testing.T) {
	model, dim, count, ok := embedding.BakedStats()
	if !ok {
		t.Skip("no baked anchor table in this tree; run EMBEDGEN=1 go test ./thread -run TestGenerateBakedAnchors")
	}
	if want := "qwen3-embedding-4b"; model != want {
		t.Errorf("baked table is for %q, but the pinned model is %q — regenerate", model, want)
	}
	t.Logf("baked table: model=%s dim=%d vectors=%d (%.2f MB as float32)",
		model, dim, count, float64(count*dim*4)/1024/1024)

	texts := bakedTexts(builtinSkillCandidates(t))
	var missing int
	for _, text := range texts {
		if !embedding.BakedHas(text) {
			missing++
			if missing <= 5 {
				t.Errorf("NOT BAKED: %q", firstRunes(text, 70))
			}
		}
	}
	if missing > 5 {
		t.Errorf("... and %d more missing", missing-5)
	}
	if missing > 0 {
		t.Errorf("%d/%d anchor texts are absent from the table — regenerate with "+
			"EMBEDGEN=1 go test ./thread -run TestGenerateBakedAnchors", missing, len(texts))
	}
}

// TestAnchorIndexNeedsNoNetwork is the property the whole file exists for,
// asserted the only way that cannot be faked: the backend it is given is
// unreachable and its key is invalid, so any vector that comes back came from
// the table.
//
// It is spelled with OpenRouter's model name on purpose. That route rejects a
// request carrying the full anchor set — measured 0/8 on 85 inputs, still 429
// after 210s of silence — which is why destructive and search never had a
// working index on the three deployments that fall back to it. If this test
// passes, that request is no longer made at all.
func TestAnchorIndexNeedsNoNetwork(t *testing.T) {
	if _, _, _, ok := embedding.BakedStats(); !ok {
		t.Skip("no baked anchor table in this tree")
	}
	dead := embedding.NewChain(func() *embedding.Backend {
		return &embedding.Backend{
			Name: "openrouter",
			// Port 1 is unassignable; a request here cannot succeed.
			URL:    "http://127.0.0.1:1/embeddings",
			APIKey: "not-a-key",
			Model:  "qwen/qwen3-embedding-4b",
		}
	})

	for _, tc := range []struct {
		name  string
		texts []string
	}{
		{"destructive", instructedAnchors(destructiveEmbedTask, destructivePosAnchors, destructiveNegAnchors)},
		{"search", instructedAnchors(searchEmbedTask, searchPosAnchors, searchNegAnchors)},
		{"coder", append(append([]string{}, coderPosAnchors...), coderNegAnchors...)},
		{"skill-none", skillNoneAnchors},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vecs, err := dead.Embed(context.Background(), tc.texts)
			if err != nil {
				t.Fatalf("%d anchors needed the network: %v", len(tc.texts), err)
			}
			if len(vecs) != len(tc.texts) {
				t.Fatalf("got %d vectors for %d texts", len(vecs), len(tc.texts))
			}
			for i, v := range vecs {
				if len(v) == 0 {
					t.Fatalf("anchor %d came back empty", i)
				}
			}
			t.Logf("%d anchors served from the table, zero requests", len(tc.texts))
		})
	}

	// And the complement: a text nobody baked must still go to the network,
	// which here means failing. Otherwise a table that silently answered
	// everything would pass the cases above for the wrong reason.
	if _, err := dead.Embed(context.Background(), []string{"这句话没有被预先嵌入过 " + t.Name()}); err == nil {
		t.Error("an unbaked text was served from the table — the lookup is not keyed by content")
	}
}

func instructedAnchors(task string, sets ...[]string) []string {
	var out []string
	for _, s := range sets {
		for _, a := range s {
			out = append(out, qwen3Instructed(bakedModelProbe, task, a))
		}
	}
	return out
}

// TestGenerateBakedAnchors regenerates embedding/anchors.bin. It needs a
// configured embedding backend and is therefore a developer command, not CI:
//
//	EMBEDGEN=1 go test ./thread -run TestGenerateBakedAnchors -v
//
// It chunks its own requests because it is the one caller that deliberately
// bypasses the table, so it faces the same input-count ceiling the table exists
// to avoid.
func TestGenerateBakedAnchors(t *testing.T) {
	if os.Getenv("EMBEDGEN") == "" {
		t.Skip("set EMBEDGEN=1 to regenerate embedding/anchors.bin")
	}
	client := searchEmbed.client
	model, ok := client.Model(context.Background())
	if !ok {
		t.Fatal("no embedding backend configured")
	}
	t.Logf("generating against %s", model)

	texts := bakedTexts(builtinSkillCandidates(t))
	t.Logf("%d distinct anchor texts", len(texts))

	const chunk = 32
	entries := make(map[string][]float64, len(texts))
	dim := 0
	for i := 0; i < len(texts); i += chunk {
		end := min(i+chunk, len(texts))
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		vecs, err := client.EmbedRemote(ctx, texts[i:end])
		cancel()
		if err != nil {
			t.Fatalf("embed chunk [%d:%d]: %v", i, end, err)
		}
		for j, v := range vecs {
			normalize(v)
			if dim == 0 {
				dim = len(v)
			}
			if len(v) != dim {
				t.Fatalf("ragged dims: got %d, want %d", len(v), dim)
			}
			entries[texts[i+j]] = v
		}
		t.Logf("  embedded %d/%d", end, len(texts))
	}

	path := filepath.Join("..", "embedding", "anchors.bin")
	if err := embedding.WriteBakedTable(path, model, dim, entries); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	t.Logf("wrote %s: %d vectors, dim %d, %.2f MB", path, len(entries), dim, float64(st.Size())/1024/1024)
	t.Log("rebuild required before the new table takes effect (go:embed reads it at compile time)")
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
