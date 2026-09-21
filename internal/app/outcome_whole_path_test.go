package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestAnOutcomeReachesWorkingTruthAndSurvivesReplay(t *testing.T) {
	model := &scriptedLLM{replies: []string{anObservation}}
	b := newOutcomeBench(t, model, nil)
	b.twoOutcomes(t)
	b.mintExperiencesWith(t, "external", "exp_a", "exp_b")
	for i := 0; i < 7; i++ {
		if err := b.st.IncrementLifetimeTicks(); err != nil {
			t.Fatal(err)
		}
	}

	// .
	if err := b.fac.ObserveOutcomes(context.Background()); err != nil {
		t.Fatal(err)
	}
	obs := b.observations(t)
	var observation citedObservation
	for _, o := range obs {
		if len(o.Cites) == 2 {
			observation = o
		}
	}
	if observation.ID == "" {
		t.Fatalf("no observation citing the two outcomes is in the record: %+v", obs)
	}

	// .
	const statement = "I finish what I can rehearse alone"
	envelope := fmt.Sprintf(`{
  "operations": [{"op": "upsert", "id": "n1", "statement": %q, "confidence": 0.7, "evidence": [%q, "exp_a", "exp_b"]}],
  "ring3_view": "You believe you finish what you can rehearse alone."
}`, statement, observation.ID)
	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: envelope}, b.door, nil, cognitive.ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}

	type truth struct {
		standing string
		anchor   int64
	}
	read := func(st *store.Store) truth {
		t.Helper()
		beliefs, err := st.ListBeliefs()
		if err != nil {
			t.Fatal(err)
		}
		for _, bl := range beliefs {
			if bl.Statement == statement {
				standing, err := st.StandingFor(bl.ID)
				if err != nil {
					t.Fatal(err)
				}
				return truth{standing, bl.ConfirmedAtTicks}
			}
		}
		t.Fatal("the belief consolidated from the observation is missing")
		return truth{}
	}
	if got := read(b.st); got != (truth{"confirmed", 7}) {
		t.Fatalf("the belief stands %+v, want confirmed — three cited sources across two classes — anchored at tick 7", got)
	}
	if edges := b.eventsOfType(t, ledger.EventEdgeCreate); len(edges) != 3 {
		t.Fatalf("%d edge events, want the three that make the standing derivable", len(edges))
	}
	runs := b.eventsOfType(t, ledger.EventConsolidationRun)
	if len(runs) != 1 {
		t.Fatalf("%d consolidation markers, want 1", len(runs))
	}
	var run store.FacilityRunPayload
	if err := json.Unmarshal(runs[0].Payload, &run); err != nil {
		t.Fatal(err)
	}
	consumed := false
	for _, id := range run.Inputs {
		consumed = consumed || id == observation.ID
	}
	if !consumed || len(run.Confirmed) != 1 || run.Confirmed[0].Ticks != 7 {
		t.Fatalf("the marker reads %+v, want the observation among its inputs and the crossing at tick 7", run)
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 0 {
		t.Fatalf("%d experiences are still raw after the pass", len(raw))
	}

	// .
	// .
	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatal(err)
	}
	if got := read(b.st); got != (truth{"confirmed", 7}) {
		t.Fatalf("after a replay the belief stands %+v, want confirmed at tick 7", got)
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 0 {
		t.Fatalf("a replay made %d consumed experiences raw again", len(raw))
	}

	// .
	// .
	fresh, err := store.New(filepath.Join(b.dir, "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := fresh.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatal(err)
	}
	if got, _ := fresh.OutcomeCursor(); got != 0 {
		t.Fatalf("the rebuilt store has a cursor (%d) — the rig is not a lost database", got)
	}
	batch, err := fresh.NextOutcomes(64)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Outcomes) != 0 {
		t.Fatalf("after a lost database the outcomes are offered again: %+v", batch.Outcomes)
	}
	if got := read(fresh); got.standing != "confirmed" {
		t.Fatalf("in the rebuilt store the belief stands %q, want confirmed", got.standing)
	}
}
