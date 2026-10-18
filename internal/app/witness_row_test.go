package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAWitnessThatLostItsRowLeavesTheIdentityRunningAndTellsTheOperator(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatalf("the first anchor: %v", err)
	}
	anchoredAt := a.anchorer.LastAnchoredSeq()
	if _, identities := f.witness.Holds(); identities != 1 || anchoredAt == 0 {
		t.Fatalf("rig: the identity has not anchored (the witness holds %d identities, anchored at %d)", identities, anchoredAt)
	}
	sealed := a.ledger.SealedSeq()

	f.witness.Forget()

	// WHAT IS PROVED IS THE OUTCOME, NOT WHO BROUGHT IT ABOUT: the anchorer
	// runs on its own clock as well, and either pass may be the one that
	// meets the witness's answer.
	notice := fmt.Sprintf("witness_row_%d", anchoredAt)
	var said string
	for i := 0; i < 8 && said == ""; i++ {
		appendFixtureEvent(t, a, f.keyPath, fmt.Sprintf("exp_after_the_witness_forgot_%d", i))
		_ = a.anchorer.CheckAndAnchor()
		for wait := 0; wait < 40 && said == ""; wait++ {
			if content, err := a.store.OutboxContent(notice); err == nil {
				said = content
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	if said == "" {
		reason, safe := a.SafeMode()
		t.Fatalf("the operator was told nothing (notice %s); conflict %v; SAFE %v (%s)", notice, a.anchorer.IntegrityConflict(), safe, reason)
	}
	ce := a.anchorer.IntegrityConflict()
	if ce == nil || !ce.WitnessSide {
		t.Fatalf("the refusal is not latched as the witness's side: %v", ce)
	}
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("A WITNESS THAT LOST ITS ROW PUT THE IDENTITY IN SAFE: %s", reason)
	}
	for _, want := range []string{"keeps running", "not witnessed", "does not continue this record"} {
		if !strings.Contains(said, want) {
			t.Errorf("the operator's notice does not say %q: %s", want, said)
		}
	}
	if a.anchorer.LastAnchoredSeq() != anchoredAt || a.ledger.SealedSeq() != sealed {
		t.Fatalf("a head was minted or a segment closed over the refusal: anchored at %d (was %d), sealed through %d (was %d)",
			a.anchorer.LastAnchoredSeq(), anchoredAt, a.ledger.SealedSeq(), sealed)
	}
	// It runs: the record still takes what the identity writes.
	appendFixtureEvent(t, a, f.keyPath, "exp_still_running")
}
