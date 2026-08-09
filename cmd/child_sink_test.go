package cmd

import "testing"

// TestChildInfixIndexCoversForks is the delivery-label regression.
//
// Fork sessions had no branch in buildDefaultChannelSinkFor, so a
// channel-prefixed fork fell through to its channel's prefix branch and was
// handed a sink addressed to a user named "<id>:fork:<task>". That send can
// never succeed, and the wake payload told the model on every turn that its
// reply would be sent to that nonexistent user — the only delivery label in the
// tree naming a destination that does not exist. Four such sessions exist on
// this deployment's disk (cli:fork:*).
func TestChildInfixIndexCoversForks(t *testing.T) {
	cases := []struct {
		key    string
		parent string
		child  bool
	}{
		{"telegram:123:fork:planning", "telegram:123", true},
		{"cli:fork:e2e-v1430-fork", "cli", true},
		{"discord:999:threads:research", "discord:999", true},
		// A fork of a subagent already matched :threads: first and must keep
		// resolving to the same parent — this change is not allowed to move it.
		{"cli:threads:a:fork:b", "cli", true},
		{"telegram:123", "", false},
		{"cron:tidyup", "", false},
		{"cli", "", false},
	}

	for _, tc := range cases {
		idx := childInfixIndex(tc.key)
		if (idx >= 0) != tc.child {
			t.Errorf("%s: child = %v, want %v", tc.key, idx >= 0, tc.child)
			continue
		}
		if idx >= 0 && tc.key[:idx] != tc.parent {
			t.Errorf("%s: parent = %q, want %q", tc.key, tc.key[:idx], tc.parent)
		}
	}
}
