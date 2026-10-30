package app

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestAnUpdateThatChangesTheModelSaysWhatItMakesAgainBeforeItActivates(t *testing.T) {
	const one, two = "org.example.vectors", "org.example.other"
	ctx := context.Background()
	a := liveApp(t)
	threeMemories(t, a)
	s := runSource(t, a, one, working)
	checked(t, a, s)
	setChoice(a, "plugin:"+one)
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	rep, err := f.Backfill(ctx, 10)
	if err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: the chosen plugin embeds the three memories: %+v %v", rep, err)
	}
	a.rememberBackfill(rep, err)
	serving := s.ap.Embeddings.Basis()
	// A pass whose work the clock could not tell from none keeps nothing.
	if rate := a.memory.workRate.Load(); (rep.Worked > 0) != (rate != nil) || rate != nil && (rate.basis != serving || rate.perMemory != rep.Worked/3) {
		t.Fatalf("a pass that embedded keeps its work for one memory under its basis: %+v (worked %s)", rate, rep.Worked)
	}
	// A fourth memory, with no vector yet: the next pass embeds it under
	// either basis, so it is not one made again.
	if _, err := a.store.DB().Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES ('e4', 'a fourth memory of the harbor', 'observation', 1, 0, 'self', 3, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	a.memory.workRate.Store(&workRate{basis: serving, perMemory: 2 * time.Second})
	next := one + "/cdcdcdcdcdcd/f3"
	fetching := map[string]pendingMark{one: {phase: pluginhost.PhaseFetching}}

	got := a.updateRemakes(f, fetching, map[string]string{one: next})
	if r := got[one]; len(got) != 1 || r.vectors != 3 || r.about != 6*time.Second {
		t.Fatalf("an update that names another model makes the three vectors again, about three times two seconds: %+v", got)
	}
	for _, phase := range []string{pluginhost.PhaseWaiting, pluginhost.PhaseReady, lifeStarting} {
		if got := a.updateRemakes(f, map[string]pendingMark{one: {phase: phase}}, map[string]string{one: next}); got[one].vectors != 3 {
			t.Errorf("an update %s is on its way up and says so: %+v", phase, got)
		}
	}
	for name, c := range map[string]struct {
		pending map[string]pendingMark
		newest  map[string]string
	}{
		"an update that keeps the basis":   {fetching, map[string]string{one: serving}},
		"bytes not yet selected":           {fetching, map[string]string{}},
		"an attempt that was refused":      {map[string]pendingMark{one: {phase: lifeRefused}}, map[string]string{one: next}},
		"a release that is stopping":       {map[string]pendingMark{one: {phase: lifeRetiring}}, map[string]string{one: next}},
		"no update in flight":              {map[string]pendingMark{}, map[string]string{one: next}},
		"an update of another plugin only": {map[string]pendingMark{two: {phase: pluginhost.PhaseFetching}}, map[string]string{two: two + "/cdcdcdcdcdcd/f3"}},
	} {
		if got := a.updateRemakes(f, c.pending, c.newest); len(got) != 0 {
			t.Errorf("%s says nothing is made again: %+v", name, got)
		}
	}

	// Not the source in use: nothing of it is made again.
	checked(t, a, runSource(t, a, two, working))
	if got := a.updateRemakes(f, map[string]pendingMark{two: {phase: pluginhost.PhaseFetching}}, map[string]string{two: two + "/cdcdcdcdcdcd/f3"}); len(got) != 0 {
		t.Errorf("an update of a plugin that is not the source in use makes nothing again: %+v", got)
	}
	setChoice(a, "none")
	if got := a.updateRemakes(f, fetching, map[string]string{one: next}); len(got) != 0 {
		t.Errorf("with the layer off, an update makes nothing again: %+v", got)
	}
	setChoice(a, "plugin:"+one)

	// No time is said without a measurement under the serving basis.
	a.memory.workRate.Store(&workRate{basis: two + "/abababababab/f3", perMemory: 2 * time.Second})
	if r := a.updateRemakes(f, fetching, map[string]string{one: next})[one]; r.vectors != 3 || r.about != 0 {
		t.Errorf("work measured under another basis says no time: %+v", r)
	}
}

func TestTheTimeToMakeVectorsAgainCountsThePassesItTakes(t *testing.T) {
	if got := remakeTime(30, 2*time.Second, 2*time.Minute, 10*time.Minute); got != time.Minute {
		t.Errorf("work within one pass is its own time: %s", got)
	}
	if got := remakeTime(2312, 2*time.Second, 2*time.Minute, 10*time.Minute); got != 23120*time.Second {
		t.Errorf("work past one pass takes a cadence a pass: %s", got)
	}
}
