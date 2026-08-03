package embedding

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/linanwx/nagobot/logger"
)

// The pre-think anchor sets are compile-time constants and the embedding model
// is pinned, so embedding them at runtime asks a remote endpoint the same
// question on every process start and gets the same answer every time. This
// file ships the answers.
//
// That is not only a saving. The anchor build was a single request carrying the
// WHOLE set — 85 texts for destructive, 84 for search — and OpenRouter's route
// for this model rejects a request with that many inputs: measured 0/8 on an
// 85-input batch, and still 429 after 210s of complete silence, while a
// 16-input request carrying 91K tokens went through fine. The constraint is the
// number of inputs, not their size. So on every deployment without a
// SiliconFlow key those two indexes NEVER built, retried every 60s forever, and
// left <destructive> running on the verb table alone — which scores 0/15 on the
// held-out set, and whose failure direction is "an irreversible action proceeds
// unconfirmed".
//
// The table is keyed by the exact text sent to the endpoint, not by anchor name
// or skill slug. That is what makes staleness unrepresentable: an edited anchor
// or a hand-modified skill description is a DIFFERENT text, so it simply misses
// and is embedded remotely. There is no invalidation to get wrong, for the same
// reason the token cache has none — the key IS the content.
//
// Vectors are stored L2-normalized as float32. Cross-provider agreement for
// this model was measured at min cosine 0.999867 across SiliconFlow CN,
// SiliconFlow Global and OpenRouter, and float32 rounding is ~6e-8 — three
// orders below that disagreement and four below the 0.05 decision margin. So
// one table serves the whole backend chain.
//
// Regenerate with:
//
//	EMBEDGEN=1 go test ./thread -run TestGenerateBakedAnchors -v

//go:embed anchors.bin
var bakedBytes []byte

const (
	bakedMagic   = "NBEMB1\n"
	bakedKeySize = sha256.Size
)

type bakedTable struct {
	model string // normalized, e.g. "qwen3-embedding-4b"
	dim   int
	vecs  map[[bakedKeySize]byte][]float32
}

var (
	bakedOnce  sync.Once
	bakedCache *bakedTable
)

// bakedKey is the lookup key: the SHA-256 of the exact string that would be
// sent as one `input` element. Generator and reader must agree on this and
// nothing else.
func bakedKey(text string) [bakedKeySize]byte { return sha256.Sum256([]byte(text)) }

// normalizeModelName strips the provider prefix and case so that the same
// weights spelled "Qwen/Qwen3-Embedding-4B" (SiliconFlow) and
// "qwen/qwen3-embedding-4b" (OpenRouter) resolve to one identity.
func normalizeModelName(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return m
}

// loadBaked parses the embedded table once. A malformed table is reported at
// Warn and then treated as absent: every lookup misses and the caller embeds
// remotely, which is the pre-existing behaviour. It is never silent — a table
// that fails to parse is a build artifact that needs regenerating.
func loadBaked() *bakedTable {
	bakedOnce.Do(func() {
		t, err := parseBaked(bakedBytes)
		if err != nil {
			logger.Warn("embedding: baked anchor table unusable, falling back to remote embedding",
				"err", err, "bytes", len(bakedBytes))
			return
		}
		if t.model == "" || len(t.vecs) == 0 {
			// An empty table is legitimate (a tree that has never been
			// generated), but it is worth saying out loud once.
			logger.Warn("embedding: baked anchor table is empty; every anchor will be embedded remotely",
				"note", "regenerate with EMBEDGEN=1 go test ./thread -run TestGenerateBakedAnchors")
			return
		}
		bakedCache = t
		logger.Info("embedding: baked anchor table loaded", "model", t.model, "vectors", len(t.vecs), "dim", t.dim)
	})
	return bakedCache
}

// lookup returns a FRESH float64 copy. Callers normalize in place and hand the
// slice to a long-lived index, so returning the stored slice would let one
// classifier mutate another's anchors.
func (t *bakedTable) lookup(text string) ([]float64, bool) {
	v, ok := t.vecs[bakedKey(text)]
	if !ok {
		return nil, false
	}
	out := make([]float64, len(v))
	for i, f := range v {
		out[i] = float64(f)
	}
	return out, true
}

// BakedStats reports what the embedded table holds. For diagnostics and for
// the drift-guard test in thread/, which has the anchor texts but no business
// knowing the file format.
func BakedStats() (model string, dim, count int, ok bool) {
	t := loadBaked()
	if t == nil {
		return "", 0, 0, false
	}
	return t.model, t.dim, len(t.vecs), true
}

// BakedHas reports whether text would be served without a network call.
func BakedHas(text string) bool {
	t := loadBaked()
	if t == nil {
		return false
	}
	_, ok := t.vecs[bakedKey(text)]
	return ok
}

// WriteBakedTable serializes a generated table to path. Generator only — the
// daemon never writes this file.
func WriteBakedTable(path, model string, dim int, entries map[string][]float64) error {
	blob, err := encodeBaked(normalizeModelName(model), dim, entries)
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o644)
}

func parseBaked(b []byte) (*bakedTable, error) {
	if len(b) < len(bakedMagic) || string(b[:len(bakedMagic)]) != bakedMagic {
		return nil, fmt.Errorf("bad magic")
	}
	p := len(bakedMagic)

	need := func(n int) error {
		if len(b)-p < n {
			return fmt.Errorf("truncated at offset %d, want %d more bytes", p, n)
		}
		return nil
	}
	if err := need(2); err != nil {
		return nil, err
	}
	modelLen := int(binary.LittleEndian.Uint16(b[p:]))
	p += 2
	if err := need(modelLen + 8); err != nil {
		return nil, err
	}
	model := string(b[p : p+modelLen])
	p += modelLen
	dim := int(binary.LittleEndian.Uint32(b[p:]))
	p += 4
	count := int(binary.LittleEndian.Uint32(b[p:]))
	p += 4
	if dim <= 0 || dim > 1<<16 {
		return nil, fmt.Errorf("implausible dim %d", dim)
	}

	entry := bakedKeySize + dim*4
	if err := need(count * entry); err != nil {
		return nil, err
	}
	vecs := make(map[[bakedKeySize]byte][]float32, count)
	for i := 0; i < count; i++ {
		var key [bakedKeySize]byte
		copy(key[:], b[p:p+bakedKeySize])
		p += bakedKeySize
		v := make([]float32, dim)
		for j := 0; j < dim; j++ {
			v[j] = math.Float32frombits(binary.LittleEndian.Uint32(b[p:]))
			p += 4
		}
		vecs[key] = v
	}
	return &bakedTable{model: model, dim: dim, vecs: vecs}, nil
}

// encodeBaked is the writer half of the format. It lives here, next to the
// parser, so the two cannot drift; the generator in thread/ calls it.
func encodeBaked(model string, dim int, entries map[string][]float64) ([]byte, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("dim must be positive, got %d", dim)
	}
	out := make([]byte, 0, len(bakedMagic)+2+len(model)+8+len(entries)*(bakedKeySize+dim*4))
	out = append(out, bakedMagic...)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(model)))
	out = append(out, model...)
	out = binary.LittleEndian.AppendUint32(out, uint32(dim))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(entries)))

	// Sorted by key so regenerating an unchanged corpus produces an identical
	// file — a byte-stable artifact is reviewable in a diff and does not churn
	// the repository.
	keys := make([][bakedKeySize]byte, 0, len(entries))
	byKey := make(map[[bakedKeySize]byte][]float64, len(entries))
	for text, vec := range entries {
		if len(vec) != dim {
			return nil, fmt.Errorf("vector for %q has dim %d, want %d", firstRunes(text, 40), len(vec), dim)
		}
		k := bakedKey(text)
		keys = append(keys, k)
		byKey[k] = vec
	}
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i][:], keys[j][:]) < 0 })
	for _, k := range keys {
		out = append(out, k[:]...)
		for _, f := range byKey[k] {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(f)))
		}
	}
	return out, nil
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
