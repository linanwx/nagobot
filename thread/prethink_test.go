package thread

import (
	"strings"
	"testing"
)

// The action hint is the pre-think system's entire output — the one string the
// main model actually reads. Its wording is carried over verbatim from the old
// LLM-parsing path on purpose: the main model's behaviour was tuned against these
// exact sentences, and swapping the classifier underneath is already a large
// enough change without rewording the instructions too.
//
// So these tests pin the TEXT, not just the booleans. If a sentence here has to
// change, that is a deliberate prompt change and should be made as one.
func TestComposePreThinkHint(t *testing.T) {
	tests := []struct {
		name        string
		destructive bool
		search      bool
		coder       bool
		invest      bool
		webURL      bool
		slugs       []string
		wantAll     []string
		wantNone    []string
	}{
		{
			name:     "nothing fires",
			wantNone: []string{"Destructive", "Search", "Code task", "Investigator", "Web URL", "skill"},
		},
		{
			name:        "destructive",
			destructive: true,
			wantAll: []string{
				"Destructive action: fulfilling this may delete data, send/publish to others, write outside the workspace, or trigger irreversible side effects.",
				"Confirm with the user via dispatch(to=user) before executing, and prefer a dry-run or reversible path.",
			},
		},
		{
			name:    "search",
			search:  true,
			wantAll: []string{"Consider dispatching a search subagent."},
		},
		{
			name:    "coder",
			coder:   true,
			wantAll: []string{"Code task:", "Consider dispatching the coder subagent"},
		},
		{
			name:    "investigator",
			invest:  true,
			wantAll: []string{"Investigator: you must call dispatch to fan out an investigator subagent before responding to the user."},
		},
		{
			name:    "web url",
			webURL:  true,
			wantAll: []string{"Web URL present: consider using playwright to open it."},
		},
		{
			name:     "one skill is singular",
			slugs:    []string{"manage-cron"},
			wantAll:  []string{`Related skill: manage-cron. Consider use_skill("manage-cron")`},
			wantNone: []string{"Related skills:"},
		},
		{
			name:    "several skills are plural and each gets a call",
			slugs:   []string{"image", "send-image"},
			wantAll: []string{`Related skills: image, send-image.`, `use_skill("image") / use_skill("send-image")`},
		},
		{
			// Order is fixed: the irreversible warning must lead, because it is the one
			// the model has to act on before anything else.
			name:        "destructive leads",
			destructive: true,
			search:      true,
			wantAll:     []string{"Destructive action:"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := composePreThinkHint(tc.destructive, tc.search, tc.coder, tc.invest, tc.webURL, tc.slugs)
			for _, w := range tc.wantAll {
				if !strings.Contains(got, w) {
					t.Errorf("hint missing %q\ngot: %s", w, got)
				}
			}
			for _, w := range tc.wantNone {
				if strings.Contains(got, w) {
					t.Errorf("hint should not contain %q\ngot: %s", w, got)
				}
			}
			if tc.name == "nothing fires" && got != "" {
				t.Errorf("a quiet turn must produce no hint at all, got: %q", got)
			}
			if tc.name == "destructive leads" && !strings.HasPrefix(got, "Destructive action:") {
				t.Errorf("destructive must come first, got: %s", got)
			}
		})
	}
}

// TestPreThinkDroppedFields guards the five fields that were removed. Each was
// dropped for a reason recorded in prethink.go, and a hint sentence reappearing
// here means one crept back in — most likely by someone reviving the old prompt
// rather than by deliberate decision.
func TestPreThinkDroppedFields(t *testing.T) {
	hint := composePreThinkHint(true, true, true, true, true, []string{"image"})
	for _, gone := range []string{
		"Multi-step task",        // reproduced by len(msg) > 160; not worth a model
		"Tone:",                  // 83% constant, copied from USER.md
		"Possible hallucination", // dropped by decision
		"Needs verification",     // dropped by decision
		"Needs clarification",    // confusing_terminology, dropped by decision
	} {
		if strings.Contains(hint, gone) {
			t.Errorf("dropped field is back in the hint: %q", gone)
		}
	}
}
