package identity

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// citingEngine is setupEngine with a genesis that carries the identity's
// fingerprint, as a real birth's does: without it nothing is "self".
func citingEngine(t *testing.T) (*Engine, *store.Store, *ledger.Ledger, *crypto.KeyPair) {
	t.Helper()
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close(); st.Close() })
	evt, err := lg.Append(ledger.EventRing0Genesis, 0,
		map[string]string{"name": "Citing", "fingerprint": kp.Fingerprint()}, kp)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Materialize(evt); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, nil, tools.Timeouts{})
	return NewEngine(st, &testEventWriter{ledger: lg, store: st, key: kp}, ring.NewManager(), discovererAdapter{reg}, Ports{}), st, lg, kp
}

func lastOfType(t *testing.T, lg *ledger.Ledger, typ ledger.EventType) *ledger.Event {
	t.Helper()
	var last *ledger.Event
	if err := ledger.Stream(lg.Path(), func(evt *ledger.Event) error {
		if evt.Type == typ {
			e := *evt
			last = &e
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if last == nil {
		t.Fatalf("no %s in the record", typ)
	}
	return last
}

func mustUnmarshal(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func cite(identity string, seq uint64, hash string) []any {
	return []any{map[string]any{"identity": identity, "seq": seq, "entry_hash": hash}}
}

const (
	foreignIdentity = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	someHash        = "abababababababababababababababababababababababababababababababab"
)

// THE IDENTITY CAN WRITE A CITATION, on every type that carries one, through
// the verbs that mint them — and what lands in the record is what it said.
func TestAllThreeTypesCarryACitationThroughTheVerbs(t *testing.T) {
	e, _, lg, _ := citingEngine(t)
	ctx := context.Background()

	if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{
		"content": "Mira's record says the pairing went faster", "cites": cite(foreignIdentity, 1306, someHash),
	}); err != nil {
		t.Fatalf("note with a citation: %v", err)
	}
	note := lastOfType(t, lg, ledger.EventExperienceCreate)
	if got, err := ledger.ParseCitations(note.Payload); err != nil || len(got) != 1 || got[0].Seq != 1306 || got[0].Identity != foreignIdentity {
		t.Fatalf("the note's record does not carry the citation: %s (%v)", note.Payload, err)
	}
	var noteID struct {
		ID string `json:"id"`
	}
	mustUnmarshal(t, note.Payload, &noteID)

	if _, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "belief.upsert", "id": "b_pairing", "statement": "Pairing is faster for this work",
		"confidence": 0.6, "evidence_refs": noteID.ID, "cites": cite(foreignIdentity, 1306, someHash),
	}); err != nil {
		t.Fatalf("belief.upsert with a citation: %v", err)
	}
	if got, _ := ledger.ParseCitations(lastOfType(t, lg, ledger.EventBeliefUpsert).Payload); len(got) != 1 {
		t.Fatal("the belief's record does not carry the citation")
	}

	if _, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "edge.create", "id": "edge_cited", "from_id": noteID.ID, "to_id": "b_pairing", "edge_type": "SUPPORTS",
		"cites": cite(foreignIdentity, 1306, someHash),
	}); err != nil {
		t.Fatalf("edge.create with a citation: %v", err)
	}
	if got, _ := ledger.ParseCitations(lastOfType(t, lg, ledger.EventEdgeCreate).Payload); len(got) != 1 {
		t.Fatal("the edge's record does not carry the citation")
	}

	// And on no other variant: a citation that means nothing is refused,
	// not carried.
	if _, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "intention.create", "id": "int_x", "statement": "cite things", "cites": cite(foreignIdentity, 1, someHash),
	}); !errors.Is(err, ledger.ErrCitation) {
		t.Fatalf("an intention carried a citation: %v", err)
	}
	// An EMPTY list there is no citation: the schema offers cites on every
	// variant, and a writer with nothing to cite writes [].
	if _, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "intention.create", "id": "int_y", "statement": "cite nothing", "cites": []any{},
	}); err != nil {
		t.Fatalf("an intention with an empty cites list was refused: %v", err)
	}
}

// A SELF-CITATION NAMES A RECORD ALREADY IN THE RECORD, by its real hash.
// Another identity's is the author's claim and is held to the grammar
// alone: there is nothing local to check it against.
func TestASelfCitationIsCheckedAgainstTheRecordItself(t *testing.T) {
	e, _, lg, kp := citingEngine(t)
	ctx := context.Background()
	if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": "the first thing I noticed"}); err != nil {
		t.Fatal(err)
	}
	first := lastOfType(t, lg, ledger.EventExperienceCreate)
	self := kp.Fingerprint()

	note := func(cites []any) error {
		_, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": "about my own record", "cites": cites, "duplicate_ok": true})
		return err
	}
	if err := note(cite(self, first.Seq, first.EntryHash())); err != nil {
		t.Fatalf("an exact self-citation was refused: %v", err)
	}

	beforeEmpty := lg.LastSeq()
	if err := note([]any{}); err != nil {
		t.Fatalf("an empty cites list was refused: %v", err)
	}
	if lg.LastSeq() != beforeEmpty+1 {
		t.Fatalf("the note with an empty cites list was not appended: %d -> %d", beforeEmpty, lg.LastSeq())
	}
	emptyRec := lastOfType(t, lg, ledger.EventExperienceCreate)
	if !strings.Contains(string(emptyRec.Payload), `"cites":[]`) {
		t.Fatalf("the record does not carry the member as written: %s", emptyRec.Payload)
	}
	if got, err := ledger.ParseCitations(emptyRec.Payload); err != nil || len(got) != 0 {
		t.Fatalf("the stored empty list reads as %d citations, err %v", len(got), err)
	}
	before := lg.LastSeq()
	for name, cites := range map[string][]any{
		"the wrong hash":           cite(self, first.Seq, someHash),
		"a record not yet there":   cite(self, before+50, someHash),
		"the record it would be":   cite(self, before+1, someHash),
		"a friendly name":          {map[string]any{"identity": self, "seq": first.Seq, "entry_hash": first.EntryHash(), "name": "me"}},
		"an uppercase fingerprint": cite(strings.ToUpper(self), first.Seq, first.EntryHash()),
		"seventeen of them": func() (out []any) {
			for i := 0; i < 17; i++ {
				out = append(out, cite(foreignIdentity, uint64(i+1), someHash)[0])
			}
			return
		}(),
	} {
		if err := note(cites); !errors.Is(err, ledger.ErrCitation) {
			t.Errorf("%s: admitted (%v)", name, err)
		}
	}
	if lg.LastSeq() != before {
		t.Fatalf("a refused citation still appended: %d -> %d", before, lg.LastSeq())
	}
	// The same wrong hash under ANOTHER identity's fingerprint is that
	// identity's record to be wrong about: admitted, as a claim.
	if err := note(cite(foreignIdentity, first.Seq, someHash)); err != nil {
		t.Fatalf("a well-formed citation of another identity was refused: %v", err)
	}
}

// A LIVE REFUSAL NEVER BECOMES A REPLAY FAILURE. A record whose cites this
// writer would refuse — written by another implementation, or an older one
// — is in the chain, signed; replay rebuilds the projection from it all the
// same. Admission is where the grammar is held, and only there.
func TestARecordThisWriterWouldRefuseStillReplays(t *testing.T) {
	_, st, lg, kp := citingEngine(t)
	for id, cites := range map[string]any{
		"exp_malformed": "not an array at all",
		"exp_named":     []any{map[string]any{"identity": foreignIdentity, "seq": 3, "entry_hash": someHash, "name": "Nova"}},
		"exp_ghost":     cite(kp.Fingerprint(), 9999, someHash),
	} {
		// Past the door: appended and signed, never validated.
		if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{
			"id": id, "content": "written by another writer", "category": "observation", "provenance": "self", "cites": cites,
		}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lg.Append(ledger.EventBeliefUpsert, 3, map[string]any{
		"id": "b_other", "statement": "from another writer", "ring": 3, "confidence": 0.5, "cites": 42,
	}, kp); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplayFromFile(lg.Path()); err != nil {
		t.Fatalf("A LIVE REFUSAL BECAME A REPLAY FAILURE: %v", err)
	}
	for _, id := range []string{"exp_malformed", "exp_named", "exp_ghost", "b_other"} {
		if ok, err := st.EntityExists(id); err != nil || !ok {
			t.Errorf("%s did not materialize on replay: %v", id, err)
		}
	}
}
