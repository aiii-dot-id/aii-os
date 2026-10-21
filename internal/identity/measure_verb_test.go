package identity

import (
	"context"
	"strings"
	"testing"
)

func TestVerbMeasureIsObservational(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	out, err := engine.ExecuteAction(context.Background(), "verb", "measure", map[string]any{})
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	for _, want := range []string{"Outcome measurement", "process_only_turns", "not success", "source:", "unknown", "Record audit", "ungrounded beliefs", "open tensions", "Meaning layer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("readout missing %q:\n%s", want, out)
		}
	}
	// a window argument is accepted.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "measure", map[string]any{"hours": 24}); err != nil {
		t.Fatalf("measure with hours: %v", err)
	}
}

// meaningAccounting is the measure read's last line: the meaning layer's
// accounting, as the resident reads it.
func meaningAccounting(t *testing.T, e *Engine) string {
	t.Helper()
	out := mustVerb(t, e, "measure", map[string]any{})
	return out[strings.LastIndex(out, "\n")+1:]
}

// THE MEASURE READ SAYS "COMPLETE" OF VECTORS RECALL CAN USE. It counted
// a vector by its row's id, so a row whose text changed after a pass read
// as embedded and the layer as complete, while recall held that vector to
// the text it was made from and left the belief to its words. A
// resident who reads "complete" takes it
// that meaning can find everything in scope.
//
// Here a belief is embedded and its row's text then changed. Before any
// pass the read says one row awaits the backfill and counts the belief's
// store one short, in the words it has always used; the read drops
// nothing, so the vector of the old statement is still in the table.
// After the pass it says complete.
func TestMeasureCountsABeliefWhoseTextChangedAsAwaitingTheBackfillUntilThePass(t *testing.T) {
	e, st, _, origin := newCurrencyFixture(t)
	ctx := context.Background()
	upsertBelief(t, e, "b_ferry", "The ferry leaves the harbor at dawn", origin)
	if _, err := e.Instruments().Backfill(ctx, 0); err != nil {
		t.Fatal(err)
	}
	const complete = "Meaning layer — basis currency/v1, complete (vectors held / rows in scope): "
	if line := meaningAccounting(t, e); !strings.HasPrefix(line, complete) || !strings.Contains(line, "beliefs 1/1") {
		t.Fatalf("rig: after a pass over an unchanged record the layer is complete: %s", line)
	}
	vectors := func() (n int) {
		t.Helper()
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM memory_vectors`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	held := vectors()

	if _, err := st.DB().Exec(`UPDATE beliefs SET statement = 'The lantern is kept in the locker' WHERE id = 'b_ferry'`); err != nil {
		t.Fatal(err)
	}
	line := meaningAccounting(t, e)
	if !strings.HasPrefix(line, "Meaning layer — basis currency/v1, 1 rows awaiting the backfill (vectors held / rows in scope): ") || !strings.Contains(line, "beliefs 0/1") {
		t.Fatalf("the belief holds the vector of what it used to say, which recall does not use; the read must say one row awaits the backfill: %s", line)
	}
	if got := vectors(); got != held {
		t.Fatalf("the measure read changed the vectors it counts: %d to %d", held, got)
	}

	if rep, err := e.Instruments().Backfill(ctx, 0); err != nil || rep.Stale != 1 || rep.Embedded != 1 {
		t.Fatalf("the pass drops the one vector and makes it again: %+v %v", rep, err)
	}
	if line := meaningAccounting(t, e); !strings.HasPrefix(line, complete) || !strings.Contains(line, "beliefs 1/1") {
		t.Fatalf("after the pass the layer is complete again: %s", line)
	}
}
