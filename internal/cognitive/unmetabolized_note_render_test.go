package cognitive

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// refusingLedger fails the test if a render mints: none of these paths does.
type refusingLedger struct{ t *testing.T }

func (l refusingLedger) Append(typ ledger.EventType, _ int, _ any, _ string) (*ledger.Event, error) {
	l.t.Errorf("a render minted %s", typ)
	return nil, errors.New("a render mints nothing")
}

// keptOutNoteStore is a real store holding an unmetabolized note and, older than
// it, one processed reflection the renders may show.
func keptOutNoteStore(t *testing.T, ordinary string) *store.Store {
	t.Helper()
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(filepath.Join(dir, "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close() })
	st, err := store.NewForTest(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, payload := range []map[string]any{
		{"id": "exp_seen", "content": ordinary, "category": "reflection", "provenance": "self", "raw": false},
		{"id": "exp_kept_out", "content": keptOutWords, "category": "reflection", "provenance": "self", "private": true},
	} {
		evt, err := lg.Append(ledger.EventExperienceCreate, 3, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Materialize(evt); err != nil {
			t.Fatalf("materialize %v: %v", payload["id"], err)
		}
	}
	return st
}

func TestAnUnmetabolizedNotesWordsReachNoRing3Render(t *testing.T) {
	const ordinary = "an ordinary reflection the render may show"

	// The rule where it is owned: the store lists the note as a row, with
	// its words and its mark (the page shows both), and does not hand it to
	// a reader that renders.
	st := keptOutNoteStore(t, ordinary)
	all, err := st.ListExperiences(5)
	if err != nil || len(all) != 2 || all[0].ID != "exp_kept_out" || all[0].Raw != 0 || all[0].Unmetabolized != 1 || all[0].Content != keptOutWords {
		t.Fatalf("the store does not list the unmetabolized note, with its words and its mark, above a processed reflection: %+v (%v)", all, err)
	}
	forConsolidate, err := st.ListExperiencesForConsolidate(5)
	if err != nil || len(forConsolidate) != 1 || forConsolidate[0].ID != "exp_seen" {
		t.Fatalf("the store handed CONSOLIDATE's render %+v (%v), want the reflection alone", forConsolidate, err)
	}

	// shown fails unless text carries the reflection (or the render had
	// nothing to show and the test proves nothing) and not the kept-out words.
	shown := func(t *testing.T, where, text string) {
		t.Helper()
		if !strings.Contains(text, ordinary) {
			t.Fatalf("%s does not carry the ordinary reflection, so its silence about the unmetabolized note proves nothing:\n%s", where, text)
		}
		if strings.Contains(text, keptOutWords) {
			t.Fatalf("A NOTE KEPT OUT OF DREAM AND CONSOLIDATE REACHED %s:\n%s", strings.ToUpper(where), text)
		}
	}
	t.Run("the render-only model pass", func(t *testing.T) {
		st := keptOutNoteStore(t, ordinary)
		m := &mockLLM{override: "a view in prose"}
		c := NewConsolidate(st, m, refusingLedger{t}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})
		if err := c.Execute(t.Context()); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if m.calls == 0 {
			t.Fatal("the render-only pass asked the model nothing, so the test proves nothing")
		}
		shown(t, "the render pass's request", m.lastSystem+"\n"+m.lastUser)
	})
	t.Run("the deterministic render after a retirement", func(t *testing.T) {
		st := keptOutNoteStore(t, ordinary)
		rw := &mockRingWriter{}
		c := NewConsolidate(st, &mockLLM{}, refusingLedger{t}, rw, ConsolidateConfig{Threshold: 3})
		c.BeliefRetired()
		shown(t, "working truth", rw.section(ring.Ring3, "working_truth"))
	})
	t.Run("the deterministic render after a failed call", func(t *testing.T) {
		st := keptOutNoteStore(t, ordinary)
		rw := &mockRingWriter{}
		c := NewConsolidate(st, &mockLLM{err: errors.New("provider down")}, refusingLedger{t}, rw, ConsolidateConfig{Threshold: 3})
		if err := c.Execute(t.Context()); err != nil {
			t.Fatalf("execute: %v", err)
		}
		shown(t, "working truth", rw.section(ring.Ring3, "working_truth"))
	})
}
