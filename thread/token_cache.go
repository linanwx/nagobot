package thread

import (
	"encoding/binary"
	"hash/maphash"
	"sync"

	"github.com/linanwx/nagobot/provider"
)

// Per-message token memoization.
//
// EstimateMessagesTokens is O(entire conversation) and runs at least twice per
// LLM call (the context budget trim before the request, the calibration log
// after it), plus once per compression eligibility check and once per web
// history read. Measured on the deployment: a 960-message / 200K-token session
// costs 320-440ms per pass on the Hetzner box, ~160ms on an M-series Mac.
//
// Almost none of that work is new. Session entries are immutable once written,
// so between two consecutive iterations of the same turn well over 99% of the
// conversation is byte-identical — only the tail grew. Keying the per-message
// count by a hash of the message's own content turns the whole estimate from
// O(conversation) into O(new messages).
//
// Hashing is ~1/1600th the cost of tokenizing the same bytes (measured: 8.5
// GB/s for maphash vs ~5 MB/s for the o200k_base BPE), which is what makes the
// trade lopsided enough to be worth no further tuning.
//
// There is no invalidation, by construction: the key IS the content. A changed
// message is a different key, never a stale hit. The one thing that can drift
// underneath us — the size of a media file named by a <<media:...>> marker —
// was already frozen at first sight by provider's path-keyed media cache long
// before this existed, so nothing new becomes stale here.

// tokenCacheEntry is 8 bytes so the map stays compact at six figures of
// entries. contentLen is a collision discriminator, not data: a 64-bit key
// collides with probability ~3e-10 at 1e5 entries, and a mismatch simply
// recomputes rather than silently returning another message's count.
type tokenCacheEntry struct {
	tokens     int32
	contentLen int32
}

// tokenCacheGenSize caps ONE generation. Two live generations bound the cache
// at 2x this (~6 MB at 8 bytes plus map overhead) — deliberate on a 3.8 GB box
// running three bots. A var, not a const, so eviction is testable.
var tokenCacheGenSize = 50000

var (
	// A per-process random seed. Nothing is persisted, so a seed that differs
	// across restarts costs one cold pass and buys immunity to constructed
	// collisions.
	tokenCacheSeed = maphash.MakeSeed()

	tokenCacheMu  sync.RWMutex
	tokenCacheCur = make(map[uint64]tokenCacheEntry, 4096)
	// tokenCacheOld is the previous generation, kept readable so a flip costs
	// no cold pass. Two generations instead of an LRU: no per-hit bookkeeping,
	// and the hot set survives a flip by promotion on read.
	tokenCacheOld map[uint64]tokenCacheEntry
)

// hashString writes a length-prefixed string. The prefix is what keeps field
// boundaries unambiguous — without it {Role:"ab", Content:""} and
// {Role:"a", Content:"b"} hash identically.
func hashString(h *maphash.Hash, s string) {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
	_, _ = h.Write(n[:])
	_, _ = h.WriteString(s)
}

func hashUint(h *maphash.Hash, v uint64) {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], v)
	_, _ = h.Write(n[:])
}

// tokenCacheKey hashes exactly the fields estimateMessageTokensUncached reads.
//
// A field absent here is a claim that it cannot change the count — the reverse
// (a field that moves the estimate but not the key) is silent wrongness, which
// is what TestTokenCacheKeyCoversEveryEstimatedField exists to prevent.
//
// ReasoningDetails contributes its LENGTH only (len/3), so only its length is
// hashed — two different reasoning blobs of equal size genuinely estimate the
// same. Media markers ride inside Content and need no separate term.
func tokenCacheKey(m provider.Message) uint64 {
	var h maphash.Hash
	h.SetSeed(tokenCacheSeed)
	hashString(&h, m.Role)
	hashString(&h, m.Content)
	if m.ReasoningTrimmed {
		hashUint(&h, 1)
	} else {
		hashUint(&h, 0)
	}
	hashString(&h, m.ReasoningContent)
	hashUint(&h, uint64(len(m.ReasoningDetails)))
	hashString(&h, m.ToolCallID)
	hashString(&h, m.Name)
	hashUint(&h, uint64(len(m.ToolCalls)))
	for _, call := range m.ToolCalls {
		hashString(&h, call.ID)
		hashString(&h, call.Type)
		hashString(&h, call.Function.Name)
		hashString(&h, call.Function.Arguments)
	}
	return h.Sum64()
}

// lookupTokenCache returns the memoized count and whether it was found in the
// older generation (callers promote those so the hot set survives a flip).
func lookupTokenCache(key uint64, contentLen int32) (tokens int32, hit, stale bool) {
	tokenCacheMu.RLock()
	defer tokenCacheMu.RUnlock()
	if e, ok := tokenCacheCur[key]; ok && e.contentLen == contentLen {
		return e.tokens, true, false
	}
	if tokenCacheOld != nil {
		if e, ok := tokenCacheOld[key]; ok && e.contentLen == contentLen {
			return e.tokens, true, true
		}
	}
	return 0, false, false
}

func storeTokenCache(key uint64, e tokenCacheEntry) {
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()
	if len(tokenCacheCur) >= tokenCacheGenSize {
		tokenCacheOld = tokenCacheCur
		tokenCacheCur = make(map[uint64]tokenCacheEntry, tokenCacheGenSize/4)
	}
	tokenCacheCur[key] = e
}

// resetTokenCache drops both generations. Tests only — nothing in the running
// daemon needs to invalidate a cache whose key is the content itself.
func resetTokenCache() {
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()
	tokenCacheCur = make(map[uint64]tokenCacheEntry, 4096)
	tokenCacheOld = nil
}
