package channel

import "testing"

// sanitizeClientMessageID is the trust boundary for an id that gets persisted
// verbatim as a message ID, so the decoys matter more than the happy path: an
// id shaped like a store-assigned one ("{sessionKey}:{unixMillis}:{hash}"), an
// id carrying path separators, and an unbounded one.
func TestSanitizeClientMessageID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"uuid form", "web-0189a7c1-4f2e-7c3a-9b11-2d5e8f6a0c34", "web-0189a7c1-4f2e-7c3a-9b11-2d5e8f6a0c34"},
		{"fallback form", "web-m9x2k1-a8f3d0e7b2", "web-m9x2k1-a8f3d0e7b2"},
		{"surrounding space trimmed", "  web-abc  ", "web-abc"},
		{"empty", "", ""},
		{"missing prefix", "abc123", ""},
		{"prefix only is still an id", "web-", "web-"},
		{"store-shaped id rejected", "web:idtest:1709571234567:000001", ""},
		{"colon anywhere rejected", "web-a:b", ""},
		{"path separator rejected", "web-../../etc/passwd", ""},
		{"whitespace inside rejected", "web-a b", ""},
		{"non-ascii rejected", "web-消息", ""},
		{"one over the length limit", "web-" + longRunOf('a', 61), ""},
		{"at the length limit", "web-" + longRunOf('a', 60), "web-" + longRunOf('a', 60)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeClientMessageID(tc.in); got != tc.want {
				t.Errorf("sanitizeClientMessageID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func longRunOf(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
