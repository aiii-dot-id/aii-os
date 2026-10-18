package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// legalRing picks a canonical ring for an event type so these probes
// test the MATERIALIZER's refusals, not the ring-authority gate.
func legalRing(t *testing.T, typ ledger.EventType) int {
	t.Helper()
	rings := ledger.CanonicalRings(typ)
	if len(rings) == 0 {
		t.Fatalf("no canonical rings for %s", typ)
	}
	return rings[0]
}

func mustRaw(t *testing.T, m map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Preflight executes the materializer in a rollback-only transaction.
// Every event family whose materializer refuses unknown targets must fail at
// preflight too — otherwise the refused event is already durable and
// poisons every future replay.
func TestPreflightRefusesUnknownTargets(t *testing.T) {
	s := testStore(t)

	cases := []struct {
		name string
		typ  ledger.EventType
		pl   map[string]any
	}{
		{"belief.promote", ledger.EventBeliefPromote, map[string]any{"id": "ghost", "ring": 3}},
		{"belief.archive", ledger.EventBeliefArchive, map[string]any{"id": "ghost", "reason": "r"}},
		{"belief.supersede", ledger.EventBeliefSupersede, map[string]any{"old_id": "ghost", "new_id": "n1", "reason": "r"}},
		{"edge.archive", ledger.EventEdgeArchive, map[string]any{"id": "ghost"}},
		{"intention.state_change", ledger.EventIntentionStateChange, map[string]any{"id": "ghost", "state": "completed"}},
		{"commitment.state_change", ledger.EventCommitmentStateChange, map[string]any{"id": "ghost", "state": "completed"}},
	}
	for _, c := range cases {
		if err := s.ValidateEvent(c.typ, legalRing(t, c.typ), mustRaw(t, c.pl)); err == nil {
			t.Errorf("%s with an unknown target passed preflight — it would append durably and then fail materialization forever", c.name)
		}
	}
}

func TestASupersedeOfASuccessorCreatedLaterStillReplays(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)
	t.Cleanup(func() { _ = l.Close() })
	belief := func(id string) map[string]any {
		return map[string]any{"id": id, "statement": "the ferry leaves at " + id, "ring": 3, "confidence": 0.5}
	}
	for _, e := range []struct {
		typ ledger.EventType
		pl  map[string]any
	}{
		{ledger.EventBeliefUpsert, belief("dawn")},
		{ledger.EventBeliefSupersede, map[string]any{"old_id": "dawn", "new_id": "noon", "reason": "the timetable changed"}},
		{ledger.EventBeliefUpsert, belief("noon")},
	} {
		if _, err := l.Append(e.typ, 3, e.pl, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReplayFromFile(l.Path()); err != nil {
		t.Fatalf("a recorded supersede whose successor a later event creates no longer replays: %v", err)
	}
	var successor sql.NullString
	if err := s.DB().QueryRow(`SELECT superseded_by FROM beliefs WHERE id = 'dawn'`).Scan(&successor); err != nil || successor.String != "noon" {
		t.Fatalf("replay did not restore the supersession: %v %v", successor, err)
	}
}

// Duplicate-create probe: a signed create event that creates NOTHING
// (ON CONFLICT DO NOTHING) is a ledgered no-op claiming to create — the
// preflight must refuse it before it becomes durable.
func TestPreflightRefusesDuplicateCreates(t *testing.T) {
	s := testStore(t)

	// commitments.counterpart_id FK-references relationships(id): a
	// promise is TO someone who exists. Seed the counterpart first.
	seedRel := probeEvent(1, ledger.EventRelationshipUpsert, legalRing(t, ledger.EventRelationshipUpsert), map[string]any{
		"id": "op", "counterpart_name": "Peer", "counterpart_role": "peer",
		"relationship_type": "peer",
	})
	if err := s.Materialize(&seedRel); err != nil {
		t.Fatalf("seed relationship: %v", err)
	}
	seedInt := probeEvent(2, ledger.EventIntentionCreate, legalRing(t, ledger.EventIntentionCreate), map[string]any{
		"id": "i1", "statement": "s", "why": "w",
	})
	if err := s.Materialize(&seedInt); err != nil {
		t.Fatalf("seed intention: %v", err)
	}
	seedCom := probeEvent(3, ledger.EventCommitmentPromised, legalRing(t, ledger.EventCommitmentPromised), map[string]any{
		"id": "c1", "description": "d", "counterpart_id": "op",
	})
	if err := s.Materialize(&seedCom); err != nil {
		t.Fatalf("seed commitment: %v", err)
	}

	if err := s.ValidateEvent(ledger.EventIntentionCreate, legalRing(t, ledger.EventIntentionCreate),
		mustRaw(t, map[string]any{"id": "i1", "statement": "dup", "why": "dup"})); err == nil {
		t.Error("duplicate intention.create passed preflight — the ledger would record a no-op that claims to create")
	}
	if err := s.ValidateEvent(ledger.EventCommitmentPromised, legalRing(t, ledger.EventCommitmentPromised),
		mustRaw(t, map[string]any{"id": "c1", "description": "dup", "counterpart_id": "op"})); err == nil {
		t.Error("duplicate commitment.promised passed preflight — the ledger would record a no-op that claims to create")
	}
}

// A FAULT IS THE DATABASE'S; A REFUSAL IS THE RULES'. A store that could
// not run the rules said nothing about the event, and the ledger door tells
// the two apart by Fault: a BUSY in CONSOLIDATE's preflight was read as a
// refusal, and the belief's only evidence was consumed with the pass.
func TestAPreflightFaultIsNotARefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	raw, err := freshImage()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// No busy wait, so the held lock answers now rather than in five seconds.
	db, err := sql.Open("sqlite", strings.Replace(sqliteDSN(path), "busy_timeout(5000)", "busy_timeout(0)", 1))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { _ = s.Close() })
	experience := mustRaw(t, map[string]any{"id": "e1", "content": "observed"})

	// Another writer holds the database, so the preflight cannot write its
	// rollback-only candidate.
	ctx := context.Background()
	holder, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateEvent(ledger.EventExperienceCreate, 3, experience); !Fault(err) {
		t.Fatalf("a busy database read as a verdict on the event: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	// Free, the same event is admissible: the error was the database's.
	if err := s.ValidateEvent(ledger.EventExperienceCreate, 3, experience); err != nil {
		t.Fatalf("the event itself was refused: %v", err)
	}

	// The rules' refusals are not faults, a broken constraint among them.
	err = s.ValidateEvent(ledger.EventCommitmentPromised, legalRing(t, ledger.EventCommitmentPromised),
		mustRaw(t, map[string]any{"id": "c1", "description": "d", "counterpart_id": "nobody"}))
	if se := new(sqlite.Error); !errors.As(err, &se) || se.Code()&0xff != sqlite3.SQLITE_CONSTRAINT {
		t.Fatalf("inconclusive: a promise to nobody was not refused by a constraint: %v", err)
	}
	if Fault(err) {
		t.Errorf("a broken constraint read as a fault: %v", err)
	}
	err = s.ValidateEvent(ledger.EventBeliefArchive, legalRing(t, ledger.EventBeliefArchive),
		mustRaw(t, map[string]any{"id": "ghost", "reason": "r"}))
	if err == nil || Fault(err) {
		t.Errorf("an unknown target must be a refusal, not a fault: %v", err)
	}
	if Fault(fmt.Errorf("wrapped: %w", sql.ErrNoRows)) || Fault(&FrozenError{Reason: "SAFE"}) || Fault(nil) {
		t.Error("a missing row, a frozen projection or no error read as a fault")
	}
}

// Replay-poison probe (the commit.go float-ring shape): a payload the
// MATERIALIZER cannot parse (ring 3.5 into an integer field) must be
// refused at preflight — today it appends durably and every future
// boot's replay fails at that event.
func TestPreflightRefusesUnparseableBeliefPayload(t *testing.T) {
	s := testStore(t)
	raw := []byte(`{"id":"b-float","statement":"s","ring":3.5,"confidence":0.5}`)
	if err := s.ValidateEvent(ledger.EventBeliefUpsert, legalRing(t, ledger.EventBeliefUpsert), raw); err == nil {
		t.Fatal("belief payload with ring 3.5 passed preflight — valid tool input would permanently poison replay")
	}
}

// The rollback-only guarantee: a preflight that PASSES must leave no
// residue — no projection row, no mirror row — and must not block the
// same event from really materializing afterwards.
func TestPreflightLeavesNoResidue(t *testing.T) {
	s := testStore(t)

	pl := map[string]any{"id": "i-clean", "statement": "s", "why": "w"}
	ring := legalRing(t, ledger.EventIntentionCreate)
	if err := s.ValidateEvent(ledger.EventIntentionCreate, ring, mustRaw(t, pl)); err != nil {
		t.Fatalf("valid create must pass preflight: %v", err)
	}

	var rows, mirror int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM intentions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&mirror); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || mirror != 0 {
		t.Fatalf("preflight left residue: intentions=%d mirror=%d (must be rollback-only)", rows, mirror)
	}

	evt := probeEvent(1, ledger.EventIntentionCreate, ring, pl)
	if err := s.Materialize(&evt); err != nil {
		t.Fatalf("real materialization after preflight must succeed: %v", err)
	}
}

// Facility run markers fail closed (H6/#4): the preflight refuses a
// marker that consumed nothing, cites a ghost experience, cites an
// already-consumed (or unmetabolized) experience, or fabricates an
// output seq — BEFORE it can append and poison replay.
func TestPreflightRefusesDefectiveFacilityRuns(t *testing.T) {
	s := testStore(t)

	seedRaw := probeEvent(1, ledger.EventExperienceCreate, 3, map[string]any{
		"id": "x_raw", "content": "raw material",
	})
	if err := s.Materialize(&seedRaw); err != nil {
		t.Fatal(err)
	}
	seedPriv := probeEvent(2, ledger.EventExperienceCreate, 3, map[string]any{
		"id": "x_priv", "content": "kept out of DREAM and CONSOLIDATE, under the earlier name for it", "private": true,
	})
	if err := s.Materialize(&seedPriv); err != nil {
		t.Fatal(err)
	}

	ring := legalRing(t, ledger.EventConsolidationRun)
	cases := []struct {
		name string
		pl   map[string]any
	}{
		{"no inputs — a run that consumed nothing is a ledgered no-op",
			map[string]any{"inputs": []string{}, "outputs": []uint64{}}},
		{"ghost input — signed marker applies to nothing",
			map[string]any{"inputs": []string{"x_ghost"}, "outputs": []uint64{}}},
		{"unmetabolized input — a note kept out of DREAM and CONSOLIDATE is never metabolized",
			map[string]any{"inputs": []string{"x_priv"}, "outputs": []uint64{}}},
		{"fabricated output seq — provenance must name a real prior event",
			map[string]any{"inputs": []string{"x_raw"}, "outputs": []uint64{999}}},
	}
	for _, c := range cases {
		if err := s.ValidateEvent(ledger.EventConsolidationRun, ring, mustRaw(t, c.pl)); err == nil {
			t.Errorf("preflight admitted a defective run marker: %s", c.name)
		}
	}

	// The valid marker passes, lands, and consumes; a SECOND marker citing
	// the same input is double consumption and refuses live.
	good := map[string]any{"inputs": []string{"x_raw"}, "outputs": []uint64{1}}
	if err := s.ValidateEvent(ledger.EventConsolidationRun, ring, mustRaw(t, good)); err != nil {
		t.Fatalf("valid run marker must pass preflight: %v", err)
	}
	run := probeEvent(3, ledger.EventConsolidationRun, ring, good)
	if err := s.Materialize(&run); err != nil {
		t.Fatalf("valid run marker must materialize: %v", err)
	}
	if err := s.ValidateEvent(ledger.EventConsolidationRun, ring, mustRaw(t, good)); err == nil {
		t.Error("re-citing a consumed experience passed preflight — double consumption must refuse live")
	}
}

func TestPreflightHoldsCitedTurnsToTheConversationAndReplayDoesNot(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at) VALUES
		('c2','s','operator','yes',2,'2026-09-01T00:00:00Z'),
		('c3','s','resident','hello',3,'2026-09-01T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, kind, details) VALUES
		('c4','s','operator','[voice] (heard in the room, not addressed to you) lunch?',4,'2026-09-01T00:00:02Z','message','{"channel":"voice","reason":"room speech"}'),
		('c5','s','operator','👍',5,'2026-09-01T00:00:03Z','message','{"grade":{"session":"s","grade":"up"}}'),
		('c6','s','operator','the act was approved',6,'2026-09-01T00:00:04Z','message','{"channel":"plugin","actor":"operator"}')`); err != nil {
		t.Fatal(err)
	}
	experience := func(turn int) map[string]any {
		return map[string]any{"id": fmt.Sprintf("x_turn%d", turn), "content": "the operator said so", "provenance": "operator", "source_turn": turn}
	}
	operator := func(turn int) map[string]any {
		return map[string]any{"id": fmt.Sprintf("rel_turn%d", turn), "counterpart_name": "Op", "counterpart_role": "operator",
			"relationship_type": "founding_operator", "charter_text": "charter",
			"operator_approval_excerpt": "yes", "operator_approval_turn": turn, "approval_basis": "conversation_turn"}
	}
	for _, c := range []struct {
		typ  ledger.EventType
		pl   map[string]any
		want string // "" = admitted
	}{
		{ledger.EventExperienceCreate, experience(2), ""},
		{ledger.EventExperienceCreate, experience(3), `operator provenance cites turn 3 with role "resident" — not an operator turn`},
		{ledger.EventExperienceCreate, experience(99), "operator provenance cites turn 99 — no such turn: fabricated evidence fails closed"},
		{ledger.EventRelationshipUpsert, operator(2), ""},
		{ledger.EventRelationshipUpsert, operator(3), `operator approval cites turn 3 with role "resident" — not an operator turn`},
		{ledger.EventRelationshipUpsert, operator(99), "operator approval cites turn 99 — no such turn: fabricated evidence fails closed"},
		// Testimony may cite a grade and the host's line for an act of the
		// operator's: both are their acts, and a grade is made to be cited.
		// Not the room's speech.
		{ledger.EventExperienceCreate, experience(4), "operator provenance cites turn 4, which the host recorded as the room's speech, not the operator's: fails closed"},
		{ledger.EventExperienceCreate, experience(5), ""},
		{ledger.EventExperienceCreate, experience(6), ""},
		{ledger.EventRelationshipUpsert, operator(4), "operator approval cites turn 4, which is not the operator's own word to the identity (the room's speech, a grade, or a line the host wrote): fails closed"},
		{ledger.EventRelationshipUpsert, operator(5), "operator approval cites turn 5, which is not the operator's own word to the identity (the room's speech, a grade, or a line the host wrote): fails closed"},
		{ledger.EventRelationshipUpsert, operator(6), "operator approval cites turn 6, which is not the operator's own word to the identity (the room's speech, a grade, or a line the host wrote): fails closed"},
	} {
		err := s.ValidateEvent(c.typ, legalRing(t, c.typ), mustRaw(t, c.pl))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s citing %v was refused: %v", c.typ, c.pl["id"], err)
		case c.want != "" && (err == nil || err.Error() != c.want):
			t.Errorf("%s citing %v: got %v, want %q", c.typ, c.pl["id"], err, c.want)
		}
	}

	x := probeEvent(1, ledger.EventExperienceCreate, 3, map[string]any{"id": "x_once", "content": "raw material"})
	if err := s.Materialize(&x); err != nil {
		t.Fatal(err)
	}
	twice := mustRaw(t, map[string]any{"inputs": []string{"x_once", "x_once"}, "outputs": []uint64{1}})
	if err := s.ValidateEvent(ledger.EventDreamRun, legalRing(t, ledger.EventDreamRun), twice); err == nil ||
		err.Error() != `dream.run cites experience "x_once" which is not raw — double consumption, or a note kept out of DREAM and CONSOLIDATE, refused` {
		t.Errorf("a run taking one input twice: %v", err)
	}
	// Two faults: the materializer's own earlier check is the one named,
	// as it was when the double consumption was the materializer's check.
	keptOut := probeEvent(2, ledger.EventExperienceCreate, 3, map[string]any{"id": "x_kept_out", "content": "kept out", "unmetabolized": true})
	if err := s.Materialize(&keptOut); err != nil {
		t.Fatal(err)
	}
	both := mustRaw(t, map[string]any{"inputs": []string{"x_kept_out"}, "outputs": []uint64{999}})
	if err := s.ValidateEvent(ledger.EventDreamRun, legalRing(t, ledger.EventDreamRun), both); err == nil ||
		err.Error() != "dream.run cites output seq 999 at or after itself (seq 3) — outputs precede their marker" {
		t.Errorf("a run with a fabricated output and an unmetabolized input: %v", err)
	}

	// The record rebuilds with no conversation at all.
	var record []ledger.Event
	for i, pl := range []map[string]any{experience(3), experience(99), operator(99)} {
		typ := ledger.EventExperienceCreate
		if i == 2 {
			typ = ledger.EventRelationshipUpsert
		}
		record = append(record, probeEvent(uint64(i+1), typ, legalRing(t, typ), pl))
	}
	record = append(record, probeEvent(4, ledger.EventExperienceCreate, 3, map[string]any{"id": "x_twice", "content": "raw"}),
		probeEvent(5, ledger.EventDreamRun, 3, map[string]any{"inputs": []string{"x_twice", "x_twice"}, "outputs": []uint64{4}}))
	fresh := testStore(t)
	if err := fresh.ReplayAll(EventSlice(record)); err != nil {
		t.Fatalf("a record holding citations the conversation cannot confirm no longer rebuilds: %v", err)
	}
	var n int
	if err := fresh.DB().QueryRow(`SELECT COUNT(*) FROM experiences WHERE id IN ('x_turn3','x_turn99') AND provenance='operator'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rebuilt experiences = %d, %v", n, err)
	}
}

func TestPreflightHoldsAnUpsertOfTheOperatorsRelationshipToTheirApprovalWhateverRoleItNames(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at) VALUES
		('c2','s','operator','yes',2,'2026-09-01T00:00:00Z'),
		('c3','s','resident','hello',3,'2026-09-01T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	typ, ring := ledger.EventRelationshipUpsert, legalRing(t, ledger.EventRelationshipUpsert)
	founding := probeEvent(1, typ, ring, map[string]any{
		"id": "rel_op", "counterpart_name": "Quinn", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "the charter Quinn affirmed",
		"operator_approval_excerpt": "yes", "operator_approval_turn": 2, "approval_basis": "conversation_turn"})
	peer := probeEvent(2, typ, ring, map[string]any{
		"id": "rel_ola", "counterpart_name": "Ola", "counterpart_role": "peer", "relationship_type": "peer", "charter_text": "Ola and I compare notes"})
	for _, evt := range []ledger.Event{founding, peer} {
		if err := s.Materialize(&evt); err != nil {
			t.Fatal(err)
		}
	}
	// The entry: the operator's id, another role named, a new name and text.
	entry := func(id string, approval map[string]any) map[string]any {
		pl := map[string]any{"id": id, "counterpart_name": "Rue", "counterpart_role": "peer", "charter_text": "a charter nobody affirmed"}
		for k, v := range approval {
			pl[k] = v
		}
		return pl
	}
	cited := func(turn int) map[string]any {
		return map[string]any{"operator_approval_excerpt": "yes", "operator_approval_turn": turn, "approval_basis": "conversation_turn"}
	}
	for _, c := range []struct {
		what    string
		pl      map[string]any
		refused bool
		shape   bool // refused for the approval's form, by name
	}{
		{"no approval", entry("rel_op", nil), true, true},
		{"words and no turn", entry("rel_op", map[string]any{"operator_approval_excerpt": "yes"}), true, true},
		{"a turn the resident said", entry("rel_op", cited(3)), true, false},
		{"a turn that is not there", entry("rel_op", cited(99)), true, false},
		{"the operator's own word", entry("rel_op", cited(2)), false, false},
		// A relationship the record holds as a peer's owes none, as before.
		{"a peer's relationship, no approval", entry("rel_ola", nil), false, false},
		{"a new peer relationship, no approval", entry("rel_new", nil), false, false},
	} {
		err := s.ValidateEvent(typ, ring, mustRaw(t, c.pl))
		switch {
		case !c.refused && err != nil:
			t.Errorf("%s: refused: %v", c.what, err)
		case c.refused && err == nil:
			t.Errorf("%s: an entry naming another role rewrote the operator's charter past admission", c.what)
		case c.refused && errors.Is(err, ErrOperatorRelationshipUnapproved) != c.shape:
			t.Errorf("%s: refused for the approval's form = %v, want %v: %v", c.what, !c.shape, c.shape, err)
		}
	}
	charterOf := func(st *Store) (text, name, role string) {
		t.Helper()
		if err := st.DB().QueryRow(`SELECT charter_text, counterpart_name, counterpart_role FROM relationships WHERE id = 'rel_op'`).Scan(&text, &name, &role); err != nil {
			t.Fatal(err)
		}
		return
	}
	// Each refusal names what the entry owes, word for word: whose the
	// relationship is and that the entry's role does not change it, then
	// the approval's own requirement, or the turn check's own words.
	const heldAs = `an upsert of the operator's relationship requires the operator's approval: relationship "rel_op" is held as the operator's, and naming role "peer" in the entry does not change that: `
	for _, c := range []struct {
		what string
		pl   map[string]any
		says string
	}{
		{"no approval", entry("rel_op", nil), heldAs + "operator relationship requires operator_approval_excerpt — Ring 1 is not identity-unilateral"},
		{"words and no basis", entry("rel_op", map[string]any{"operator_approval_excerpt": "yes"}), heldAs + `operator relationship requires approval_basis "conversation_turn" (got "")`},
		{"words, the basis and no turn", entry("rel_op", map[string]any{"operator_approval_excerpt": "yes", "approval_basis": "conversation_turn"}), heldAs + "conversation_turn basis requires operator_approval_turn"},
		{"a turn the resident said", entry("rel_op", cited(3)), `operator approval cites turn 3 with role "resident" — not an operator turn`},
		{"a turn that is not there", entry("rel_op", cited(99)), "operator approval cites turn 99 — no such turn: fabricated evidence fails closed"},
	} {
		if err := s.ValidateEvent(typ, ring, mustRaw(t, c.pl)); err == nil || err.Error() != c.says {
			t.Errorf("%s:\n got %v\nwant %s", c.what, err, c.says)
		}
	}
	if text, name, _ := charterOf(s); text != "the charter Quinn affirmed" || name != "Quinn" {
		t.Fatalf("admission's questions changed the relationship: %q %q", text, name)
	}

	// A record that already holds such an entry: the materializer takes it
	// as it always did, appended and replayed alike, and Ring 1 reads as
	// that record says.
	unapproved := probeEvent(3, typ, ring, entry("rel_op", nil))
	if err := s.Materialize(&unapproved); err != nil {
		t.Fatalf("the materializer must not make admission's check: %v", err)
	}
	fresh := testStore(t)
	if err := fresh.ReplayAll(EventSlice([]ledger.Event{founding, peer, unapproved})); err != nil {
		t.Fatalf("a record holding an entry admitted before this check no longer rebuilds: %v", err)
	}
	for _, st := range []*Store{s, fresh} {
		if text, name, role := charterOf(st); text != "a charter nobody affirmed" || name != "Rue" || role != "operator" {
			t.Fatalf("the record's own entry must read as it did: %q %q %q", text, name, role)
		}
	}

	// ADMISSION THAT CANNOT READ WHAT ROLE THE RECORD HOLDS REFUSES. The
	// question is whether the entry writes Ring 1; an answer that could not
	// be read is not "no". The refusal names the read that failed.
	unread := testStore(t)
	if err := unread.Materialize(&founding); err != nil {
		t.Fatal(err)
	}
	if _, err := unread.DB().Exec(`ALTER TABLE relationships RENAME TO relationships_away`); err != nil {
		t.Fatal(err)
	}
	if err := unread.ValidateEvent(typ, ring, mustRaw(t, entry("rel_op", nil))); err == nil || !strings.HasPrefix(err.Error(), `verify the role relationship "rel_op" holds: `) {
		t.Errorf("an entry whose relationship's role could not be read must be refused, naming that read: %v", err)
	}
}

func TestPreflightStillValidatesRingAuthority(t *testing.T) {
	s := testStore(t)
	err := s.ValidateEvent(ledger.EventBeliefUpsert, 0, mustRaw(t, map[string]any{
		"id": "b1", "statement": "s", "ring": 0, "confidence": 0.5,
	}))
	if err == nil || !strings.Contains(err.Error(), "ring") {
		t.Fatalf("ring 0 must not be a legal authority for belief.upsert, got %v", err)
	}
}

func TestExperienceProvenanceVocabularyMatchesSchema(t *testing.T) {
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`provenance IN \(([^)]+)\)`).FindStringSubmatch(string(schemaBytes))
	if m == nil {
		t.Fatal("cannot locate the experiences.provenance CHECK in schema.sql")
	}
	schemaSet := map[string]bool{}
	for _, tok := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(m[1], -1) {
		schemaSet[tok[1]] = true
	}
	if len(schemaSet) == 0 {
		t.Fatal("parsed an empty provenance vocabulary from schema.sql")
	}

	s := testStore(t)
	// operator provenance live-verifies its cited turn — seed a real one.
	if err := s.AddConversationTurn("operator", "vocabulary probe"); err != nil {
		t.Fatal(err)
	}
	opTurn, err := s.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("seeded operator turn not found: %v", err)
	}

	ring := legalRing(t, ledger.EventExperienceCreate)
	probe := func(id, provenance string) error {
		pl := map[string]any{"id": id, "content": "c", "provenance": provenance}
		switch provenance {
		case "operator":
			pl["source_turn"] = opTurn.TurnSeq
		case "external":
			pl["source_url"] = "https://example.test/source"
		}
		return s.ValidateEvent(ledger.EventExperienceCreate, ring, mustRaw(t, pl))
	}

	i := 0
	for tok := range schemaSet {
		i++
		if err := probe(fmt.Sprintf("prov_ok_%d", i), tok); err != nil {
			t.Errorf("schema-sanctioned provenance %q refused by preflight: %v", tok, err)
		}
	}

	for _, dead := range []string{"work", "no_such_provenance"} {
		if schemaSet[dead] {
			continue // the vocabulary grew — this probe is no longer dead
		}
		err := probe("prov_dead_"+dead, dead)
		if err == nil {
			t.Fatalf("provenance %q passed preflight but is outside the schema CHECK — a real mint would die as a raw SQL error", dead)
		}
		msg := err.Error()
		if strings.Contains(msg, "constraint") || strings.Contains(msg, "CHECK") {
			t.Fatalf("provenance %q died as a raw constraint error, not a sanctioned refusal: %v", dead, err)
		}
		idx := strings.Index(msg, "sanctioned: ")
		if idx < 0 {
			t.Fatalf("provenance %q refusal lacks the sanctioned-vocabulary listing: %v", dead, err)
		}
		msgSet := map[string]bool{}
		for _, tok := range strings.Split(msg[idx+len("sanctioned: "):], ",") {
			msgSet[strings.TrimSpace(tok)] = true
		}
		for tok := range schemaSet {
			if !msgSet[tok] {
				t.Errorf("sanctioned listing omits schema token %q: %v", tok, err)
			}
		}
		for tok := range msgSet {
			if !schemaSet[tok] {
				t.Errorf("sanctioned listing advertises %q, which the schema CHECK refuses: %v", tok, err)
			}
		}
	}
}

// A SELF-SUPERSESSION ALREADY RECORDED STILL REPLAYS. The refusal is the
// live door's (validateSupersedeSuccessorLocked); a ledger that holds one
// from before replays as it did, the belief superseded by itself.
func TestARecordedSelfSupersessionStillReplays(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)
	t.Cleanup(func() { _ = l.Close() })
	for _, e := range []struct {
		typ ledger.EventType
		pl  map[string]any
	}{
		{ledger.EventBeliefUpsert, map[string]any{"id": "dawn", "statement": "the ferry leaves at dawn", "ring": 3, "confidence": 0.5}},
		{ledger.EventBeliefSupersede, map[string]any{"old_id": "dawn", "new_id": "dawn", "reason": "restated"}},
		{ledger.EventBeliefUpsert, map[string]any{"id": "noon", "statement": "the ferry leaves at noon", "ring": 3, "confidence": 0.5}},
	} {
		if _, err := l.Append(e.typ, 3, e.pl, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReplayFromFile(l.Path()); err != nil {
		t.Fatalf("a recorded self-supersession no longer replays: %v", err)
	}
	var successor sql.NullString
	if err := s.DB().QueryRow(`SELECT superseded_by FROM beliefs WHERE id = 'dawn'`).Scan(&successor); err != nil || successor.String != "dawn" {
		t.Fatalf("replay did not restore the recorded self-supersession: %v %v", successor, err)
	}
	// A current belief, so nothing but the rule can refuse it.
	if err := s.ValidateEvent(ledger.EventBeliefSupersede, legalRing(t, ledger.EventBeliefSupersede),
		mustRaw(t, map[string]any{"old_id": "noon", "new_id": "noon", "reason": "restated"})); err == nil || !strings.Contains(err.Error(), "its own successor") {
		t.Fatalf("the live door did not refuse a self-supersession by its rule: %v", err)
	}
}

// AN ENTRY UNDER A SUPERSEDED OPERATOR'S ID OWES THE APPROVAL TOO: fail
// closed. Admission asks the role the record holds under the entry's id and
// does not ask whether that relationship was superseded since, so a row
// that was the operator's charter before a successor took over still holds
// "operator", and an entry naming another role under it still owes the
// operator's word. Nothing is written under the operator's name that the
// operator did not affirm, whichever operator it was.
func TestAnEntryUnderASupersededOperatorsIdOwesTheApprovalToo(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at) VALUES
		('c2','s','operator','yes',2,'2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	typ, ring := ledger.EventRelationshipUpsert, legalRing(t, ledger.EventRelationshipUpsert)
	approved := map[string]any{"operator_approval_excerpt": "yes", "operator_approval_turn": 2, "approval_basis": "conversation_turn"}
	with := func(pl map[string]any) map[string]any {
		for k, v := range approved {
			pl[k] = v
		}
		return pl
	}
	first := probeEvent(1, typ, ring, with(map[string]any{
		"id": "rel_op_first", "counterpart_name": "Quinn", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "the first charter"}))
	second := probeEvent(2, typ, ring, with(map[string]any{
		"id": "rel_op_second", "counterpart_name": "Quinn", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "the charter that replaced it", "supersedes": "rel_op_first"}))
	for _, evt := range []ledger.Event{first, second} {
		if err := s.Materialize(&evt); err != nil {
			t.Fatal(err)
		}
	}
	var successor sql.NullString
	if err := s.DB().QueryRow(`SELECT superseded_by FROM relationships WHERE id = 'rel_op_first'`).Scan(&successor); err != nil || successor.String != "rel_op_second" {
		t.Fatalf("rig: the first operator relationship must be superseded by the second: %v %v", successor, err)
	}
	err := s.ValidateEvent(typ, ring, mustRaw(t, map[string]any{
		"id": "rel_op_first", "counterpart_name": "Rue", "counterpart_role": "peer", "charter_text": "a charter nobody affirmed"}))
	if !errors.Is(err, ErrOperatorRelationshipUnapproved) {
		t.Fatalf("AN ENTRY UNDER A SUPERSEDED OPERATOR'S ID WAS ADMITTED WITHOUT THE OPERATOR'S WORD, or refused for another reason: %v", err)
	}
	if err := s.ValidateEvent(typ, ring, mustRaw(t, with(map[string]any{
		"id": "rel_op_first", "counterpart_name": "Rue", "counterpart_role": "peer", "charter_text": "affirmed this time"}))); err != nil {
		t.Fatalf("the same entry with the operator's word is refused: %v", err)
	}
}
