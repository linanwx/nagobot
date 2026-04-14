package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	openai "github.com/openai/openai-go/v3"
)

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
