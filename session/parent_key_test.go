package session

import "testing"

// TestImmediateParentKey pins the parent a child's end-of-turn notice goes to:
// the session that dispatched it, which for a nested child is the intermediate
// child rather than the root. Forks must resolve too: a channel-prefixed fork
// once fell through to its channel's prefix and was treated as a channel user
// named "<id>:fork:<task>".
func TestImmediateParentKey(t *testing.T) {
	cases := []struct {
		key    string
		parent string
		child  bool
	}{
		{"telegram:123:fork:planning", "telegram:123", true},
		{"cli:fork:e2e-v1430-fork", "cli", true},
		{"discord:999:threads:research", "discord:999", true},
		{"cli:threads:a:fork:b", "cli:threads:a", true},
		{"cli:fork:a:threads:b", "cli:fork:a", true},
		{"telegram:123", "", false},
		{"cron:tidyup", "", false},
		{"cli", "", false},
	}
	for _, tc := range cases {
		parent, ok := ImmediateParentKey(tc.key)
		if ok != tc.child || parent != tc.parent {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.key, parent, ok, tc.parent, tc.child)
		}
	}
}
