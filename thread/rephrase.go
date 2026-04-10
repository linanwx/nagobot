package thread

import (
	"strings"

	"github.com/linanwx/nagobot/session"
)

// isRephraseSession reports whether the session key is a rephrase sibling.
func isRephraseSession(key string) bool {
	return strings.HasSuffix(key, session.RephraseSessionSuffix)
}
