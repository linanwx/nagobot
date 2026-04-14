package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/logger"
	openai "github.com/openai/openai-go/v3"
)

type sessionKeyCtxKey struct{}

// WithSessionKey returns a ctx carrying the sessionKey for diagnostic logging.
func WithSessionKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKeyCtxKey{}, key)
}

// SessionKeyFromContext retrieves a sessionKey previously set via WithSessionKey.
func SessionKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(sessionKeyCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// dumpFirstMessage writes messages[0] to disk under
// {configDir}/logs/prefix-dump/{sanitized-sessionKey}/{m1hash}.json so
// prefix drift between turns can be diffed offline. The filename is the
// content hash, so identical prefixes idempotently overwrite the same file
// (no bloat), while drift produces a new file per variant.
func dumpFirstMessage(providerName, sessionKey string, msgs []openai.ChatCompletionMessageParamUnion) {
	if len(msgs) == 0 {
		return
	}
	b, err := json.Marshal(msgs[0])
	if err != nil {
		return
	}
	m1 := hashBytes(b)

	cd, err := config.ConfigDir()
	if err != nil || cd == "" {
		return
	}
	safeKey := sanitizeSessionKeyForFS(sessionKey)
	if safeKey == "" {
		safeKey = "_unknown"
	}
	dir := filepath.Join(cd, "logs", "prefix-dump", providerName, safeKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	path := filepath.Join(dir, m1+".json")
	if _, err := os.Stat(path); err == nil {
		return // same hash already dumped — skip
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		logger.Warn("prefix-dump write failed", "provider", providerName, "sessionKey", sessionKey, "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		logger.Warn("prefix-dump rename failed", "provider", providerName, "sessionKey", sessionKey, "err", err)
		_ = os.Remove(tmp)
		return
	}
}

func sanitizeSessionKeyForFS(key string) string {
	if key == "" {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(key))
	for _, r := range key {
		switch {
		case r == ':' || r == '/' || r == '\\' || r == ' ':
			sb.WriteByte('_')
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:4])
}

// hashChatToolParams serializes the tools array and returns (sha256 prefix, count).
// Used to detect whether the tools portion of the prompt-cache prefix drifts between turns.
func hashChatToolParams(tools []openai.ChatCompletionToolUnionParam) (string, int) {
	b, err := json.Marshal(tools)
	if err != nil {
		return "error", len(tools)
	}
	return hashBytes(b), len(tools)
}

// hashChatMessagePrefixes returns a compact hash string for messages[:k]
// at k ∈ {1, 2, 4, 8, ..., 2^i ≤ N} ∪ {N}, used to locate the exact
// message index where the cache prefix diverges from the previous turn.
func hashChatMessagePrefixes(msgs []openai.ChatCompletionMessageParamUnion) string {
	n := len(msgs)
	if n == 0 {
		return ""
	}

	ks := make([]int, 0, 16)
	for k := 1; k <= n; k *= 2 {
		ks = append(ks, k)
	}
	if ks[len(ks)-1] != n {
		ks = append(ks, n)
	}

	var sb strings.Builder
	for i, k := range ks {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(k))
		sb.WriteByte(':')
		b, err := json.Marshal(msgs[:k])
		if err != nil {
			sb.WriteString("error")
			continue
		}
		sb.WriteString(hashBytes(b))
	}
	return sb.String()
}
