package provider

import (
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

var (
	tiktokenOnce  sync.Once
	tiktokenCodec tokenizer.Codec
)

func getCodec() tokenizer.Codec {
	tiktokenOnce.Do(func() {
		enc, err := tokenizer.Get(tokenizer.O200kBase)
		if err != nil {
			return
		}
		tiktokenCodec = enc
	})
	return tiktokenCodec
}

// EstimateTextTokens returns a tiktoken-based token estimate for a string.
func EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	codec := getCodec()
	if codec == nil {
		return len(text) / 3
	}
	ids, _, _ := codec.Encode(text)
	return len(ids)
}
