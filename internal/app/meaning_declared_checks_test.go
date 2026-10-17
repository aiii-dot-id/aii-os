package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

func TestASourceThatDeclaresItsChecksIsNotProbedAgain(t *testing.T) {
	declared := &packagefmt.Validation{Checks: []packagefmt.Check{{Name: "embeds probe 1", Sets: []string{packagefmt.AllSets}, Lane: packagefmt.LaneEmbeddings}}}

	passed := liveApp(t)
	s := runSource(t, passed, "org.example.vectors", working)
	s.ap.DeclareChecksForTest(declared, &pluginfacility.Checks{Ran: 1})
	checked(t, passed, s)
	if pick := passed.pickMeaningSource(); pick.kind != "plugin" || s.asked() != 0 {
		t.Fatalf("a source whose declared checks passed is the source, unasked: %+v, %d calls", pick.off, s.asked())
	}

	pending := liveApp(t)
	p := runSource(t, pending, "org.example.vectors", working)
	p.ap.DeclareChecksForTest(declared, &pluginfacility.Checks{Ran: 1, Failed: &pluginfacility.CheckFailure{Check: "embeds probe 1", Outcome: pluginfacility.CheckTimedOut, Words: "answered as its publisher says in 812 ms, past its bound of 500 ms"}})
	checked(t, pending, p)
	pick := pending.pickMeaningSource()
	if pick.kind != "" || pick.off == nil || pick.off.Kind != meaningOffProbe || !strings.Contains(pick.off.Detail, "have not yet passed") || !strings.Contains(pick.off.Detail, "embeds probe 1") {
		t.Fatalf("a source whose declared checks have not yet passed is off and says why: %+v", pick)
	}
	for i := 0; i < 3; i++ {
		pending.pickMeaningSource()
	}
	if p.asked() != 0 {
		t.Fatalf("a source that declares its checks was probed: %d calls", p.asked())
	}
}
