package identity

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// A PAGE IN WORKING TRUTH, RECALLED, THROUGH THE REAL RECORD: a page the
// resident fetched and noted, since processed, is the newest thing working
// truth lists; the section is rendered from the store as a retirement
// renders it, and read back by the recall verb, listed and searched. The
// frame is whole in both answers and the mark of the cut stands after it.
func TestARecallNeverShowsAnOutsideSourceInWorkingTruthWithItsFrameLeftOpen(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	page := "A page about tides. " + strings.Repeat("and more of the page ", 20)
	evt, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp_page", "content": page, "category": "observation",
		"provenance": "external", "source_url": "https://example.org/tides", "raw": false,
	}, kp)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Materialize(evt); err != nil {
		t.Fatal(err)
	}
	var shown []string
	c := cognitive.NewConsolidate(st, recordingConsolidation{&shown}, nil, cognitive.NewPersistingRingWriter(e.rings, nil), cognitive.ConsolidateConfig{Threshold: 3})
	c.BeliefRetired()

	truth := e.rings.Section(ring.Ring3, "working_truth")
	open, end := strings.Index(truth, untrusted.Open), strings.Index(truth, untrusted.Close)
	if open < 0 || end < open || utf8.RuneCountInString(truth[:open+len(untrusted.Open)]) > 200 || utf8.RuneCountInString(truth[:end]) < 240 {
		t.Fatalf("working truth does not open a frame early and close it past what a recall shows of a section, so the test proves nothing:\n%s", truth)
	}

	for name, args := range map[string]map[string]any{
		"listed":   {},
		"searched": {"query": "tides"},
	} {
		out := mustVerb(t, e, "recall", args)
		at := strings.Index(out, "[ring3/working_truth]")
		if at < 0 || !strings.Contains(out[at:], untrusted.Open) {
			t.Fatalf("%s: the answer does not show working truth and its opening marker, so the test proves nothing:\n%s", name, out)
		}
		// Every region the answer opens, it closes before another opens
		// or the answer ends.
		for rest := out; ; {
			i := strings.Index(rest, untrusted.Open)
			if i < 0 {
				break
			}
			rest = rest[i+len(untrusted.Open):]
			end, next := strings.Index(rest, untrusted.Close), strings.Index(rest, untrusted.Open)
			if end < 0 || next >= 0 && next < end {
				t.Fatalf("%s: THE ANSWER OPENS AN UNTRUSTED REGION AND DOES NOT CLOSE IT; what follows it stands inside:\n%s", name, out)
			}
			rest = rest[end+len(untrusted.Close):]
		}
		if !strings.Contains(out[at:], untrusted.Close+"...") {
			t.Fatalf("%s: the mark of the cut does not stand after the frame, in the host's own words:\n%s", name, out)
		}
	}
}
