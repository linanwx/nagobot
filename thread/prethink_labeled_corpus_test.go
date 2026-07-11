package thread

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Each row is a real multilingual user message scored by the production pre-think
// agent itself (deepseek-v4-flash, thinking off — the exact model the `fast`
// specialty resolves to). The target is therefore the behaviour these detectors
// replace, rather than my opinion of what that behaviour should be.
//
// AGREEMENT IS NOT CORRECTNESS. This is the trap in the whole file and it has
// already caught me twice:
//
//   - has_web_url scores 71% "precision" here. All four "false positives" are real
//     https:// links that the LLM failed to notice. The detector is right and the
//     label is wrong, so its true precision is 100%.
//   - search scores 26% "recall". Inspect the misses and most are the LLM
//     over-firing: a physical constant (线膨胀系数), a settled historical fact, a
//     request to write fiction, and "what version of chatgpt are you" — which the
//     search detector excludes on purpose. Faithfully reproducing that number would
//     mean reproducing the mistakes.
//
// So read a low agreement as "these two disagree; go look at why", never as "the
// detector is broken".
//
// is_multi_step is absent from the scoreboard because the field itself is gone. The
// LLM's verdict there turned out to be, in effect, len(message) > 160 — and an
// embedding classifier built for it scored BELOW an always-false baseline. A field
// whose entire content is "the user wrote a lot" was deleted rather than replaced.
// IsMultiStep stays in the row struct below: the labels still carry it, and it is
// the evidence for that decision.
//
// Regenerate with scratchpad/label_prethink.py; point PRETHINK_LABELS at the
// resulting JSONL. Every test here skips when it is unset.
//
// Base rates over 200 messages, worth keeping in view when reading any accuracy
// number below — a field at 2% is trivially 98% "accurate" by saying false:
//
//	is_multi_step 34.0%  hallucination 20.0%  search 17.5%  confusing_terminology 15.0%
//	has_web_url 5.0%  needs_verification 2.0%  is_include_investigator 0.5%  destructive 0.0%
//
// The LLM is self-consistent (same prompt, temperature 0, run twice: is_multi_step
// flips 1/80, search 4/80, the rest 0-2/80), so these labels are a stable function
// of the message, not noise. Where a detector disagrees, someone is actually wrong.
type labeledRow struct {
	Lang                 string `json:"lang"`
	Text                 string `json:"text"`
	IsMultiStep          bool   `json:"is_multi_step"`
	IsIncludeInvestigato bool   `json:"is_include_investigator"`
	HasWebURL            bool   `json:"has_web_url"`
	ConfusingTerminology bool   `json:"confusing_terminology"`
	Destructive          bool   `json:"destructive"`
	Hallucination        bool   `json:"hallucination"`
	Search               bool   `json:"search"`
	NeedsVerification    bool   `json:"needs_verification"`
}

func loadLabels(t *testing.T) []labeledRow {
	t.Helper()
	path := os.Getenv("PRETHINK_LABELS")
	if path == "" {
		t.Skip("set PRETHINK_LABELS=/path/prethink_labels.jsonl")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open labels: %v", err)
	}
	defer f.Close()

	var rows []labeledRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		var r labeledRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad label row: %v", err)
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Fatal("no labeled rows")
	}
	return rows
}

// agreement scores a local detector against the LLM's own verdicts. Accuracy
// alone is a trap on a skewed field, so precision and recall are always printed
// next to it, along with the base rate that makes them readable.
type agreement struct {
	tp, fp, tn, fn int
}

func (a agreement) String() string {
	n := a.tp + a.fp + a.tn + a.fn
	prec, rec := 0.0, 0.0
	if a.tp+a.fp > 0 {
		prec = float64(a.tp) / float64(a.tp+a.fp)
	}
	if a.tp+a.fn > 0 {
		rec = float64(a.tp) / float64(a.tp+a.fn)
	}
	return fmt.Sprintf("agree %.1f%% | precision %.0f%% (%d/%d) | recall %.0f%% (%d/%d) | LLM said true %d/%d",
		100*float64(a.tp+a.tn)/float64(n),
		100*prec, a.tp, a.tp+a.fp,
		100*rec, a.tp, a.tp+a.fn,
		a.tp+a.fn, n)
}

func score(rows []labeledRow, want func(labeledRow) bool, got func(labeledRow) bool) agreement {
	var a agreement
	for _, r := range rows {
		w, g := want(r), got(r)
		switch {
		case w && g:
			a.tp++
		case !w && g:
			a.fp++
		case !w && !g:
			a.tn++
		default:
			a.fn++
		}
	}
	return a
}

// TestDetectorsVsLLM is the scoreboard: every localized field, measured against
// the LLM it replaces, on the same 200 messages.
//
//	PRETHINK_LABELS=/path/prethink_labels.jsonl go test ./thread -run VsLLM -v
func TestDetectorsVsLLM(t *testing.T) {
	rows := loadLabels(t)

	t.Logf("has_web_url             %v", score(rows,
		func(r labeledRow) bool { return r.HasWebURL },
		func(r labeledRow) bool { return hasWebURL(r.Text) }))

	t.Logf("is_include_investigator %v", score(rows,
		func(r labeledRow) bool { return r.IsIncludeInvestigato },
		func(r labeledRow) bool { return isIncludeInvestigator(r.Text) }))

	t.Logf("search                  %v", score(rows,
		func(r labeledRow) bool { return r.Search },
		func(r labeledRow) bool { return needsSearch(r.Text) }))

	t.Logf("destructive             %v", score(rows,
		func(r labeledRow) bool { return r.Destructive },
		func(r labeledRow) bool { return isDestructive(r.Text, "") }))
}
