package thread

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomHex returns a random lowercase hex string of length n*2.
func RandomHex(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
