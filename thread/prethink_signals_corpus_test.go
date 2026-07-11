package thread

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Corpus sweep for the local pre-think signal detectors. The hand-written cases
// in prethink_signals_test.go prove the cases we thought of; this one surfaces
// the ones we did not, by running a detector over real multilingual user
// messages and printing what it matched.
//
// Skipped unless PRETHINK_CORPUS points at a JSONL file of {"lang","text"} rows
// (e.g. first-turn user messages sampled from allenai/WildChat-1M):
//
//	PRETHINK_CORPUS=/path/sample.jsonl go test ./thread -run Corpus -v
func TestHasWebURL_Corpus(t *testing.T) {
	rows := loadCorpus(t)

	var hits int
	for _, r := range rows {
		if !hasWebURL(r.Text) {
			continue
		}
		hits++
		for _, m := range webURLRE.FindAllString(r.Text, 3) {
			if len(m) > 90 {
				m = m[:90]
			}
			t.Logf("match [%s] %q", r.Lang, m)
		}
	}
	t.Logf("hasWebURL flagged %d/%d messages (%.1f%%)", hits, len(rows), pct(hits, len(rows)))
}

func TestIsIncludeInvestigator_Corpus(t *testing.T) {
	rows := loadCorpus(t)

	var hits int
	for _, r := range rows {
		if !isIncludeInvestigator(r.Text) {
			continue
		}
		hits++
		trigger := investigatorAskRE.FindString(investigatorMaskRE.ReplaceAllString(r.Text, " "))
		txt := []rune(r.Text)
		if len(txt) > 100 {
			txt = txt[:100]
		}
		t.Logf("trigger=%q [%s] %s", trigger, r.Lang, string(txt))
	}
	t.Logf("isIncludeInvestigator flagged %d/%d messages (%.1f%%)", hits, len(rows), pct(hits, len(rows)))
}

// Unlike the two fields above, <search> has no cheap visual oracle — you cannot
// glance at a message and instantly know whether the answer rests on a fact that
// went stale. So this sweep prints BOTH directions: what fired (precision) and a
// sample of what did not (recall, the dangerous side — a miss means the model
// states a stale fact with confidence).
func TestNeedsSearch_Corpus(t *testing.T) {
	rows := loadCorpus(t)

	var hits int
	for _, r := range rows {
		flagged := needsSearch(r.Text)
		if flagged {
			hits++
		}
		if os.Getenv("SHOW") == "miss" && flagged {
			continue
		}
		if os.Getenv("SHOW") == "hit" && !flagged {
			continue
		}
		if os.Getenv("SHOW") == "" {
			continue
		}
		trigger := searchSignalRE.FindString(r.Text)
		txt := []rune(r.Text)
		if len(txt) > 110 {
			txt = txt[:110]
		}
		t.Logf("trigger=%q [%s] %s", trigger, r.Lang, string(txt))
	}
	t.Logf("needsSearch flagged %d/%d messages (%.1f%%)", hits, len(rows), pct(hits, len(rows)))
}

type corpusRow struct {
	Lang string `json:"lang"`
	Text string `json:"text"`
}

func loadCorpus(t *testing.T) []corpusRow {
	t.Helper()

	path := os.Getenv("PRETHINK_CORPUS")
	if path == "" {
		t.Skip("PRETHINK_CORPUS not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("corpus unreadable: %v", err)
	}
	defer f.Close()

	var rows []corpusRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var r corpusRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if r.Text != "" {
			rows = append(rows, r)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan corpus: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("corpus empty")
	}
	return rows
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// TestIsDestructive_Corpus is the false-alarm audit. The corpus is chatbot
// traffic — users talking to a model with no tools — so a genuinely destructive
// request is rare in it. That makes it a poor recall test and an excellent
// PRECISION test, which is the half that hand-written cases cannot police: a
// detector tuned for recall on 67 cases I invented will happily fire on the
// 400 phrasings I did not.
//
// Every hit printed here is a turn where the assistant would stop and ask the
// user to confirm. Read them as interruptions, not as matches.
func TestIsDestructive_Corpus(t *testing.T) {
	rows := loadCorpus(t)

	var hits int
	for _, r := range rows {
		if !isDestructive(r.Text, "") {
			continue
		}
		hits++
		if os.Getenv("SHOW") == "hit" {
			t.Logf("[%s] %s", r.Lang, truncRunes(collapse(r.Text), 100))
		}
	}
	t.Logf("destructive fired on %d/%d (%.1f%%) — each one is a confirmation prompt",
		hits, len(rows), 100*float64(hits)/float64(len(rows)))
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
