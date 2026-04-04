package provider

import "sync"

// mediaTokenCache caches estimated token counts by file path.
// Shared by image, audio, and PDF token estimation to avoid repeated file reads.
var mediaTokenCache sync.Map // path (string) → tokens (int)

func cachedEstimate(path string, compute func(string) int) int {
	if v, ok := mediaTokenCache.Load(path); ok {
		return v.(int)
	}
	tokens := compute(path)
	mediaTokenCache.Store(path, tokens)
	return tokens
}
