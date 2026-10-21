package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// cardEmbedder is a source of vectors for the card's tests: a basis, a
// floor of its own, and one small vector a text.
type cardEmbedder struct {
	basis string
	floor float64
	fail  error
}

func (e cardEmbedder) Basis() (string, error) { return e.basis, nil }
func (e cardEmbedder) Floor() float64         { return e.floor }
func (e cardEmbedder) Embed(_ context.Context, _ memory.Kind, inputs []string) (string, [][]float32, error) {
	if e.fail != nil {
		return e.basis, nil, e.fail
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{1, float32(i)}
	}
	return e.basis, out, nil
}

// THE MEMORY VIEW'S CARD SAYS WHAT THE MEANING LAYER IS BUILT FROM AND HOW
// MUCH OF THE RECORD IT COVERS. Until now the only place an operator could
// learn either was a refusal a resident read, or the log. The card is the
// host's own reading at the moment it is asked:
//
//   - with no source, the layer is off and the reason is the one a
//     resident reads, so the two cannot disagree;
//   - with a source, its basis, the floor that is that source's own, and
//     for each store the rows in scope and how many hold a vector, before
//     a backfill and after one;
//   - the last pass since this start: when it ended, what it embedded,
//     and why it stopped if it stopped. A pass with no source to ask is
//     not a pass and leaves nothing.
func TestTheMeaningCardReadsSourceBasisFloorCoverageAndTheLastPass(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for seq := 1; seq <= 3; seq++ {
		if _, err := st.DB().Exec(`INSERT OR IGNORE INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'test.event', 3, '{}', '')`, seq, now); err != nil {
			t.Fatal(err)
		}
	}
	for i, content := range []string{"the ship reached the harbor at dusk", "a quiet day of routine observations"} {
		if _, err := st.DB().Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, 0, 'self', ?, ?)`,
			"e"+string(rune('1'+i)), content, i+1, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b1', 'Tides govern the harbor', 3, 0.8, 0, 3, 3)`); err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: &Config{}}
	f := memory.New(st)
	ctx := context.Background()

	// No source: off, with the resident's own reason, and nothing else.
	off, err := a.meaningState(ctx, f, a.pickMeaningSource())
	if err != nil {
		t.Fatal(err)
	}
	if off.Source != "" || off.Unavailable == "" || off.Why != meaningOffNoSource || off.Basis != "" || off.Coverage != nil || off.LastPass != nil {
		t.Fatalf("with no source the card must say off and why, and nothing more: %+v", off)
	}
	if _, _, reason, _ := f.HeldCoverage(ctx); reason != off.Unavailable {
		t.Fatalf("the card's reason %q is not the one the measure read gives %q", off.Unavailable, reason)
	}

	// A source whose floor is its own.
	source := cardEmbedder{basis: "Local/bge-m3", floor: 0.3}
	f.SetEmbedder(source)
	coverage := func() map[string][2]int64 {
		t.Helper()
		state, err := a.meaningState(ctx, f, meaningPick{source: source, kind: "provider"})
		if err != nil {
			t.Fatal(err)
		}
		if state.Source != "provider" || state.Basis != "Local/bge-m3" || state.Floor != 0.3 || state.Unavailable != "" {
			t.Fatalf("the card must name the source, its basis and its own floor: %+v", state)
		}
		got := map[string][2]int64{}
		for _, c := range state.Coverage {
			got[c.Store] = [2]int64{c.Embedded, c.Rows}
		}
		return got
	}
	before := coverage()
	if before["experiences"] != [2]int64{0, 2} || before["beliefs"] != [2]int64{0, 1} {
		t.Fatalf("before any backfill no row holds a vector: %v", before)
	}
	if state, _ := a.meaningState(ctx, f, meaningPick{source: source, kind: "provider"}); state.LastPass != nil {
		t.Fatalf("no pass has run since this start: %+v", state.LastPass)
	}

	// One pass, kept as the owner keeps it.
	rep, err := f.Backfill(ctx, 2)
	a.rememberBackfill(rep, err)
	after := coverage()
	if after["experiences"][0]+after["beliefs"][0] != 2 {
		t.Fatalf("a pass of two leaves two rows holding a vector: %v", after)
	}
	state, _ := a.meaningState(ctx, f, meaningPick{source: source, kind: "provider"})
	if state.LastPass == nil || state.LastPass.Embedded != 2 || state.LastPass.Stopped != "" {
		t.Fatalf("the last pass embedded two and did not stop: %+v", state.LastPass)
	}
	if at, err := time.Parse(time.RFC3339, state.LastPass.At); err != nil || time.Since(at) > time.Minute {
		t.Fatalf("the last pass is timed now: %q %v", state.LastPass.At, err)
	}

	// A pass the source failed: what landed stays, and the card says why it stopped.
	failing := cardEmbedder{basis: "Local/bge-m3", floor: 0.3, fail: errors.New("the endpoint refused the request")}
	f.SetEmbedder(failing)
	rep, err = f.Backfill(ctx, 2)
	if err == nil {
		t.Fatal("rig: a failing source must fail the pass")
	}
	a.rememberBackfill(rep, err)
	state, _ = a.meaningState(ctx, f, meaningPick{source: failing, kind: "provider"})
	if state.LastPass == nil || state.LastPass.Stopped == "" {
		t.Fatalf("a pass that stopped says why: %+v", state.LastPass)
	}

	// A pass with no source to ask leaves the last real pass in place.
	f.SetEmbedder(nil)
	rep, err = f.Backfill(ctx, 2)
	a.rememberBackfill(rep, err)
	if kept := a.memory.lastBackfill.Load(); kept == nil || kept.stopped == "" {
		t.Fatalf("a pass with no source replaced the last pass that had one: %+v", kept)
	}
}
