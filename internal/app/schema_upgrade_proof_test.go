package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const proofPortrait = "I keep the lessons in my recorded experience."
const proofContinuity = "I remain curious and careful."

type reflectionUpgradeProof struct {
	b       *cognitionBench
	legacy  bool
	event   ledger.Event
	history []byte
}

func newReflectionUpgradeProof(t *testing.T, legacy bool) *reflectionUpgradeProof {
	t.Helper()
	b := newCognitionBench(t)
	sources := []struct {
		typ     ledger.EventType
		ring    int
		payload map[string]any
	}{
		{ledger.EventBeliefUpsert, 3, map[string]any{
			"id": "proof_b", "statement": "Follow the evidence.", "ring": 3, "confidence": 0.8}},
		{ledger.EventExperienceCreate, 3, map[string]any{
			"id": "proof_x", "content": "A synthetic failure taught me something.", "category": "observation"}},
		{ledger.EventIntentionCreate, 3, map[string]any{
			"id": "proof_i", "statement": "Keep learning."}},
		// The peer makes a current-format initial portrait admissible.
		// SELF_MODEL reads the OPERATOR relationship, so this peer does
		// not give its next pass a fourth class if the portrait is lost.
		{ledger.EventRelationshipUpsert, 1, map[string]any{
			"id": "proof_peer", "counterpart_name": "Synthetic Peer", "counterpart_role": "peer",
			"relationship_type": "peer", "charter_text": "Learn together."}},
	}
	if !legacy {
		// Current creation uses main's atomic evidence-origin contract. The
		// historical case retains its original originless event and ordering.
		sources[0], sources[1] = sources[1], sources[0]
		sources[1].payload["origin_refs"] = []map[string]string{{"kind": "experience", "id": "proof_x"}}
	}
	for _, source := range sources {
		if legacy {
			evt, err := b.lg.Append(source.typ, source.ring, source.payload, b.kp)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.st.Materialize(evt); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := b.door.Append(source.typ, source.ring, source.payload, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	var evt *ledger.Event
	var err error
	if legacy {
		// Fixture manufacture only: the historic writer used this shape.
		// New production writes still pass through the strict native door.
		evt, err = b.lg.Append(ledger.EventSelfModelSynthesize, 3, map[string]any{
			"id": "proof_prior", "content": proofPortrait, "source_seqs": []uint64{},
			"source_entity_refs": []string{"proof_b", "proof_x", "proof_i"},
			"changes_since_last": "I learned from a failure.", "continuity_thread": proofContinuity,
		}, b.kp)
	} else {
		evt, err = b.door.Append(ledger.EventSelfModelSynthesize, 3, map[string]any{
			"id": "proof_prior", "synthesis_text": proofPortrait, "continuity_thread": proofContinuity,
			"source_entity_refs": []ledger.SelfModelSourceRef{
				{Class: "beliefs", ID: "proof_b"}, {Class: "experiences", ID: "proof_x"},
				{Class: "intentions", ID: "proof_i"}, {Class: "relationships", ID: "proof_peer"},
			},
		}, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ledger.VerifyChain(b.ledgerP, b.kp.PublicKeyBytes(), nil); err != nil || n != 5 {
		t.Fatalf("fixture record does not verify: %d events, %v", n, err)
	}
	raw, err := os.ReadFile(b.ledgerP)
	if err != nil {
		t.Fatal(err)
	}
	return &reflectionUpgradeProof{b: b, legacy: legacy, event: *evt, history: raw}
}

func TestSchemaUpgradeReflectionCurrentControl(t *testing.T) {
	p := newReflectionUpgradeProof(t, false)
	seedReflectionRuntime(t, p.b.st.DB())
	p.exercise(t, p.b.st, filepath.Join(p.b.dir, "store.db"))
}

// Historical decoding must remain a replay capability, not a way to mint
// today's events under yesterday's admission. An error after append is too
// late: the ledger door must reject this before any signed bytes are written.
func TestSchemaUpgradeReflectionHistoricalShapeCannotMint(t *testing.T) {
	p := newReflectionUpgradeProof(t, false)
	before, err := os.ReadFile(p.b.ledgerP)
	if err != nil {
		t.Fatal(err)
	}
	seq := p.b.lg.LastSeq()
	evt, err := p.b.door.Append(ledger.EventSelfModelSynthesize, 3, map[string]any{
		"id": "proof_new_legacy", "content": "A new write must meet today's contract.",
		"source_seqs": []uint64{}, "source_entity_refs": []string{"proof_b"},
		"continuity_thread": proofContinuity, "changes_since_last": "This is a new request.",
		"previous_synthesis_id": "proof_prior",
	}, "")
	if err == nil || evt != nil {
		t.Errorf("new historical-shaped request was not refused before append: event returned=%t err=%v", evt != nil, err)
	}
	after, readErr := os.ReadFile(p.b.ledgerP)
	if readErr != nil || !bytes.Equal(before, after) || p.b.lg.LastSeq() != seq {
		t.Errorf("refusal changed signed history: seq=%d, want %d; %v", p.b.lg.LastSeq(), seq, readErr)
	}
	current, readErr := p.b.st.CurrentSelfModel()
	if readErr != nil {
		t.Fatal(readErr)
	}
	p.checkPortrait(t, current)
	var rejectedRows int
	if err := p.b.st.DB().QueryRow(`SELECT COUNT(*) FROM self_model_synthesis WHERE id='proof_new_legacy'`).Scan(&rejectedRows); err != nil || rejectedRows != 0 {
		t.Fatalf("rejected synthesis reached the projection: rows=%d, err=%v", rejectedRows, err)
	}
}

// A one-portrait fixture cannot catch a decoder that restores every old
// portrait as current, or drops earlier portraits when making the latest
// one current. Exercise two recorded portraits, then a native successor.
func TestSchemaUpgradeReflectionSuccession(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current_format_control"
		if legacy {
			name = "historical_portraits_then_native_successor"
		}
		t.Run(name, func(t *testing.T) {
			p := newReflectionUpgradeProof(t, legacy)
			modern := func(id, previous string) map[string]any {
				return map[string]any{
					"id": id, "previous_synthesis_id": previous,
					"synthesis_text": "I build on " + previous + ".", "continuity_thread": proofContinuity,
					"source_entity_refs": []ledger.SelfModelSourceRef{
						{Class: "beliefs", ID: "proof_b"}, {Class: "experiences", ID: "proof_x"},
						{Class: "intentions", ID: "proof_i"}, {Class: "reflections", ID: previous},
					},
				}
			}
			var second *ledger.Event
			var err error
			if legacy {
				// The historical writer supplied previous_synthesis_id, but
				// its materializer made the latest event current in ledger
				// order. No verification_status event followed it.
				second, err = p.b.lg.Append(ledger.EventSelfModelSynthesize, 3, map[string]any{
					"id": "proof_second", "previous_synthesis_id": "proof_prior",
					"content": "I build on proof_prior.", "continuity_thread": proofContinuity,
					"changes_since_last": "I learned from my earlier portrait.",
					"source_seqs":        []uint64{}, "source_entity_refs": []string{"proof_b", "proof_prior"},
				}, p.b.kp)
			} else {
				second, err = p.b.door.Append(ledger.EventSelfModelSynthesize, 3, modern("proof_second", "proof_prior"), "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if n, err := ledger.VerifyChain(p.b.ledgerP, p.b.kp.PublicKeyBytes(), nil); err != nil || n != 6 {
				t.Fatalf("two-portrait fixture does not verify: count=%d err=%v", n, err)
			}
			prefix, err := os.ReadFile(p.b.ledgerP)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(p.b.dir, "succession.db")
			s, err := store.NewForTest(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReplayFromFile(p.b.ledgerP); err != nil {
				t.Fatalf("two recorded portraits cannot replay: %v", err)
			}
			seedReflectionRuntime(t, s.DB())
			current, err := s.CurrentSelfModel()
			if err != nil || current == nil || current.ID != "proof_second" || current.SynthesisText != "I build on proof_prior." || current.CreatedSeq != second.Seq || current.CreatedAt != second.Timestamp {
				t.Fatalf("latest recorded portrait is not current: %+v err=%v", current, err)
			}
			if legacy {
				wantRefs := []ledger.SelfModelSourceRef{{ID: "proof_b"}, {ID: "proof_prior"}}
				if !reflect.DeepEqual(current.SourceEntityRefs, wantRefs) {
					t.Fatalf("historical citations changed or gained an invented class: %+v", current.SourceEntityRefs)
				}
			}
			portraits, err := s.ListSelfModelSyntheses(10, 0)
			if err != nil || len(portraits) != 2 {
				t.Fatalf("historical succession lost a portrait: count=%d err=%v", len(portraits), err)
			}
			p.checkPortrait(t, &portraits[1])
			door := &ledgerAdapter{Ledger: p.b.lg, kp: p.b.kp, st: s}
			if _, err := door.Append(ledger.EventSelfModelSynthesize, 3, modern("proof_successor", "proof_second"), ""); err != nil {
				t.Fatalf("native successor could not continue the recorded succession: %v", err)
			}
			if n, err := ledger.VerifyChain(p.b.ledgerP, p.b.kp.PublicKeyBytes(), nil); err != nil || n != 7 {
				t.Fatalf("continued succession does not verify: count=%d err=%v", n, err)
			}
			if after, err := os.ReadFile(p.b.ledgerP); err != nil || !bytes.HasPrefix(after, prefix) || len(after) <= len(prefix) {
				t.Fatalf("continuation changed the signed history prefix: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{path, filepath.Join(p.b.dir, "succession-cold.db")} {
				t.Run(filepath.Base(target), func(t *testing.T) {
					reopened, err := store.NewForTest(target)
					if err != nil {
						t.Fatal(err)
					}
					defer reopened.Close()
					if err := reopened.ReplayFromFile(p.b.ledgerP); err != nil {
						t.Fatal(err)
					}
					latest, err := reopened.CurrentSelfModel()
					if err != nil || latest == nil || latest.ID != "proof_successor" {
						t.Fatalf("native successor is not current after replay: %+v err=%v", latest, err)
					}
					items, err := reopened.ListSelfModelSyntheses(10, 0)
					if err != nil || len(items) != 3 || !reflect.DeepEqual(items[1], *current) {
						t.Fatalf("prior succession changed after replay: count=%d err=%v", len(items), err)
					}
					p.checkPortrait(t, &items[2])
					for previous, next := range map[string]string{"proof_prior": "proof_second", "proof_second": "proof_successor"} {
						var got string
						if err := reopened.DB().QueryRow(`SELECT superseded_by FROM self_model_synthesis WHERE id=?`, previous).Scan(&got); err != nil || got != next {
							t.Errorf("succession %s -> %q, want %s: %v", previous, got, next, err)
						}
					}
					if target == path {
						checkReflectionRuntime(t, reopened)
					}
				})
			}
		})
	}
}

// Restore must compare the original database's acknowledgment with the
// verified record BEFORE replay replaces the mirror. Comparing only the
// replay result with its own input cannot detect a missing accepted tail.
// This is the production database restore-proof entry point, not a full
// restore ceremony; its caller owns signature/witness verification.
func TestSchemaUpgradeRestorePreservesAcknowledgedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		shortened bool
		lagging   bool
	}{
		{name: "matching_pair_control"},
		{name: "lagging_projection_is_repaired", lagging: true},
		{name: "valid_prefix_behind_mirror", shortened: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReflectionUpgradeProof(t, false)
			seedReflectionRuntime(t, p.b.st.DB())
			if tc.lagging {
				// A projection behind a complete record is recoverable;
				// requiring identical pre-replay heads would break it.
				events, err := ledger.ReadAll(p.b.ledgerP)
				if err != nil {
					t.Fatal(err)
				}
				if err := p.b.st.ReplayAll(store.EventSlice(events[:len(events)-1])); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(p.b.dir, "store.db")
			if err := p.b.st.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			history := p.history
			wantSeq := p.event.Seq
			if tc.shortened {
				lines := bytes.SplitAfter(history, []byte{'\n'})
				if len(lines) != 6 || len(lines[5]) != 0 {
					t.Fatal("fixture must have five complete signed records")
				}
				history = bytes.Join(lines[:4], nil)
				wantSeq--
			}
			record := filepath.Join(p.b.dir, "restore-record.jsonl")
			if err := os.WriteFile(record, history, 0600); err != nil {
				t.Fatal(err)
			}
			var verified store.MirrorHead
			if n, err := ledger.VerifyChainVisiting(record, p.b.kp.PublicKeyBytes(), nil, func(evt *ledger.Event) error {
				verified = store.MirrorHead{Seq: evt.Seq, Hash: evt.EntryHash()}
				return nil
			}); err != nil || uint64(n) != wantSeq {
				t.Fatalf("restore fixture must be a valid signed record: count=%d want=%d err=%v", n, wantSeq, err)
			}
			scratch := filepath.Join(p.b.dir, "restore-proof")
			head, facts, err := store.ProveRestore(t.Context(), path, record, scratch, verified)
			if tc.shortened {
				if err == nil {
					t.Errorf("restore accepted a prefix ending at %d after erasing acknowledgment of %d", head.Seq, p.event.Seq)
				}
			} else {
				if err != nil || head.Seq != wantSeq || head.Hash != p.event.EntryHash() {
					t.Errorf("matching pair did not restore: head=%+v err=%v", head, err)
				}
				if facts.Ephemeral["conversations"] != 1 || facts.Ephemeral["work_sessions"] != 1 {
					t.Errorf("restore lost retained runtime rows: %+v", facts)
				}
			}
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
				t.Errorf("restore proof modified the source database: %v", err)
			}
			if after, err := os.ReadFile(record); err != nil || !bytes.Equal(after, history) {
				t.Errorf("restore proof modified the source record: %v", err)
			}
			if _, err := os.Stat(scratch); !os.IsNotExist(err) {
				t.Errorf("restore proof did not remove its scratch directory: %v", err)
			}
		})
	}
}

func TestSchemaUpgradeReflectionHistoricalTable(t *testing.T) {
	p := newReflectionUpgradeProof(t, true)
	path := p.historicalDatabase(t)
	s, err := store.OpenAndReplay(t.Context(), path, store.MirrorHead{Seq: p.event.Seq, Hash: p.event.EntryHash()}, func(yield func(*ledger.Event) error) error { return ledger.Stream(p.b.ledgerP, yield) })
	if err != nil {
		// Opening may already have committed destructive schema work.
		// Observe the failed attempt through the read-only mount.
		old, readErr := store.OpenReadOnly(path)
		if readErr != nil {
			t.Errorf("failed attempt no longer has a readable database: %v", readErr)
		} else {
			var mirrored int
			if readErr := old.DB().QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&mirrored); readErr != nil || mirrored != 5 {
				t.Errorf("failed open changed the acknowledged mirror: rows=%d, want 5; %v", mirrored, readErr)
			}
			old.Close()
		}
		if after, readErr := os.ReadFile(p.b.ledgerP); readErr != nil || !bytes.Equal(after, p.history) {
			t.Errorf("failed open changed the signed source: %v", readErr)
		}
		t.Fatalf("historical database cannot reach reflection readers: %v", err)
	}
	defer s.Close()
	p.exercise(t, s, path)
}

func TestSchemaUpgradeReflectionColdReplay(t *testing.T) {
	p := newReflectionUpgradeProof(t, true)
	path := filepath.Join(p.b.dir, "fresh.db")
	s, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReplayFromFile(p.b.ledgerP); err != nil {
		t.Fatalf("verified historical reflection cannot rebuild without its old table: %v", err)
	}
	seedReflectionRuntime(t, s.DB())
	p.exercise(t, s, path)
}

func TestSchemaUpgradeReflectionHistoricalReferenceReaders(t *testing.T) {
	p := newReflectionUpgradeProof(t, true)
	s := p.b.st
	e := p.event
	// A representation probe, distinct from the actual old-table upgrade:
	// preserve cited IDs with no invented class in the current SQL shape.
	// It prevents an outer schema/decoder failure from hiding reader drift.
	if _, err := s.DB().Exec(`INSERT INTO ledger (seq,prev,ts,type,ring,payload,content)
		VALUES (?,?,?,?,?,?,?)`, e.Seq, e.Prev, e.Timestamp, e.Type, e.Ring, string(e.Payload), e.Content); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO self_model_synthesis
		(id,synthesis_text,source_entity_refs,changes_since_last,continuity_thread,created_seq,created_at)
		VALUES ('proof_prior',?,'[{"id":"proof_b"},{"id":"proof_x"},{"id":"proof_i"}]',?,?,?,?)`,
		proofPortrait, "I learned from a failure.", proofContinuity, e.Seq, e.Timestamp); err != nil {
		t.Fatal(err)
	}
	seedReflectionRuntime(t, s.DB())
	p.exercise(t, s, filepath.Join(p.b.dir, "store.db"))
}

func (p *reflectionUpgradeProof) historicalDatabase(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "store", "testdata", "schema-pre-work-queue.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != "c388c8fd65867632a4ad9b6845d2942b239dbcbdf13f5f16fbf6d9d23e22b943" {
		t.Fatalf("historical schema is not the exact sanitized fixture: %s", got)
	}
	path := filepath.Join(p.b.dir, "historical.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	events, err := ledger.ReadAll(p.b.ledgerP)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if _, err := db.Exec(`INSERT INTO ledger
			(seq,prev_hash,timestamp,type,author,ring,payload,content_hash,signature,sig_key_id)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, e.Seq, e.Prev, e.Timestamp, e.Type, p.b.kp.Fingerprint(),
			e.Ring, string(e.Payload), e.Content, e.Sig, p.b.kp.Fingerprint()); err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`INSERT INTO beliefs (id,statement,ring,confidence,first_seq,last_seq) VALUES ('proof_b','Follow the evidence.',3,0.8,1,1)`,
		`INSERT INTO experiences (id,content,category,created_seq,created_at) VALUES ('proof_x','A synthetic failure taught me something.','observation',2,'2026-08-16T00:00:00Z')`,
		`INSERT INTO intentions (id,statement,created_seq) VALUES ('proof_i','Keep learning.',3)`,
		`INSERT INTO relationships (id,counterpart_name,counterpart_role,relationship_type,charter_text,created_seq) VALUES ('proof_peer','Synthetic Peer','peer','peer','Learn together.',4)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO reflections
		(id,content,source_seqs,source_entity_refs,changes_since_last,continuity_thread,verification_status,created_seq,created_at)
		VALUES ('proof_prior',?,'[]','["proof_b","proof_x","proof_i"]',?,?,'pending',?,?)`,
		proofPortrait, "I learned from a failure.", proofContinuity, p.event.Seq, p.event.Timestamp); err != nil {
		t.Fatal(err)
	}
	seedReflectionRuntime(t, db)
	return path
}

func seedReflectionRuntime(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO conversations (id,session_id,role,content,turn_seq,created_at) VALUES ('proof_turn','proof_session','operator','Keep my synthetic conversation.',7,'2026-08-16T00:00:00Z')`,
		`INSERT INTO work_sessions (id,description,status,result) VALUES ('proof_work','Keep my synthetic work.','delivered','A retained result.')`,
		`INSERT INTO identity_lifetime (singleton_id,birth_at,lifetime_ticks,last_tick_at) VALUES ('current','2026-08-16T00:00:00Z',71,'2026-08-16T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func checkReflectionRuntime(t *testing.T, s *store.Store) {
	t.Helper()
	var conversation, work, result string
	var ticks int64
	if err := s.DB().QueryRow(`SELECT content FROM conversations WHERE id='proof_turn'`).Scan(&conversation); err != nil || conversation != "Keep my synthetic conversation." {
		t.Fatalf("conversation changed: %q, %v", conversation, err)
	}
	if err := s.DB().QueryRow(`SELECT description,result FROM work_sessions WHERE id='proof_work'`).Scan(&work, &result); err != nil || work != "Keep my synthetic work." || result != "A retained result." {
		t.Fatalf("work changed: %q / %q, %v", work, result, err)
	}
	if err := s.DB().QueryRow(`SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id='current'`).Scan(&ticks); err != nil || ticks != 71 {
		t.Fatalf("lived clock changed: %d, %v", ticks, err)
	}
}

// recallPortrait finds the one portrait its words match through recall's
// own route, then reads it whole.
func recallPortrait(t *testing.T, s *store.Store, words string) *store.SelfModelSynthesis {
	t.Helper()
	hits, _, err := memory.New(s).Enumerate(context.Background(), "self_model_synthesis", words, false, 0, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("the portrait is not recallable by %q: %+v, %v", words, hits, err)
	}
	items, err := s.ListSelfModelSyntheses(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		if items[i].ID == hits[0].ID {
			return &items[i]
		}
	}
	t.Fatalf("recall found %s, which the list does not hold", hits[0].ID)
	return nil
}

func (p *reflectionUpgradeProof) checkPortrait(t *testing.T, item *store.SelfModelSynthesis) {
	t.Helper()
	if item == nil || item.ID != "proof_prior" || item.SynthesisText != proofPortrait || item.ContinuityThread != proofContinuity || item.CreatedSeq != p.event.Seq || item.CreatedAt != p.event.Timestamp {
		t.Fatalf("historical portrait changed or disappeared: %+v", item)
	}
	if p.legacy && item.ChangesSinceLast != "I learned from a failure." {
		t.Fatalf("historical changes-since-last were lost: %q", item.ChangesSinceLast)
	}
	want := map[string]string{"proof_b": "beliefs", "proof_x": "experiences", "proof_i": "intentions", "proof_peer": "relationships"}
	if p.legacy {
		want = map[string]string{"proof_b": "", "proof_x": "", "proof_i": ""}
	}
	if len(item.SourceEntityRefs) != len(want) {
		t.Fatalf("recorded references lost or invented: %+v", item.SourceEntityRefs)
	}
	for _, ref := range item.SourceEntityRefs {
		class, ok := want[ref.ID]
		if !ok || ref.Class != class {
			t.Fatalf("recorded reference changed: %+v", ref)
		}
		delete(want, ref.ID)
	}
}

func (p *reflectionUpgradeProof) exercise(t *testing.T, s *store.Store, path string) {
	t.Helper()
	checkReflectionRuntime(t, s)
	t.Run("current", func(t *testing.T) {
		current, err := s.CurrentSelfModel()
		if err != nil {
			t.Fatal(err)
		}
		p.checkPortrait(t, current)
	})
	t.Run("list", func(t *testing.T) {
		items, err := s.ListSelfModelSyntheses(10, 0)
		if err != nil || len(items) != 1 {
			t.Fatalf("list: %v, %v", items, err)
		}
		p.checkPortrait(t, &items[0])
	})
	t.Run("search", func(t *testing.T) {
		p.checkPortrait(t, recallPortrait(t, s, "recorded experience"))
	})
	t.Run("prompt", func(t *testing.T) {
		composer := prompt.New(ring.NewManager(), 32000)
		composer.SetIdentitySource(s)
		built, err := composer.Compose("", 0)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(built.Text, proofPortrait) {
			t.Fatal("the current portrait did not reach the prompt")
		}
	})
	t.Run("next_synthesis_and_restart", func(t *testing.T) {
		p.continueAndRestart(t, s, path)
	})
}

type reflectionProofLLM struct{ calls int }

func (m *reflectionProofLLM) Chat(_ context.Context, messages []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	m.calls++
	var evidence string
	for _, message := range messages {
		evidence += message.Content
	}
	if !strings.Contains(evidence, "[reflections id=proof_prior]") || !strings.Contains(evidence, proofPortrait) {
		return nil, fmt.Errorf("the prior portrait was not supplied to SELF_MODEL")
	}
	args := map[string]any{
		"variant": "self_model.synthesize", "id": "proof_next", "previous_synthesis_id": "proof_prior",
		"synthesis_text":    "I can continue learning without losing my earlier self-understanding.",
		"continuity_thread": proofContinuity,
		"source_entity_refs": []ledger.SelfModelSourceRef{
			{Class: "beliefs", ID: "proof_b"}, {Class: "experiences", ID: "proof_x"},
			{Class: "intentions", ID: "proof_i"}, {Class: "reflections", ID: "proof_prior"},
		},
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var call llm.ToolCall
	call.Type, call.Function.Name, call.Function.Arguments = "function", "commit", string(raw)
	return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{call}}}}, ModelID: "proof-model"}, nil
}

func (p *reflectionUpgradeProof) continueAndRestart(t *testing.T, s *store.Store, path string) {
	t.Helper()
	door := &ledgerAdapter{Ledger: p.b.lg, kp: p.b.kp, st: s}
	engine := identity.NewEngine(s, door, ring.NewManager(), nil, identity.Ports{})
	before := p.b.lg.LastSeq()
	if _, err := engine.ExecuteAction(t.Context(), "verb", "commit", map[string]any{
		"variant": "self_model.synthesize", "id": "proof_invalid", "previous_synthesis_id": "proof_prior",
		"synthesis_text": "A new event must meet the current contract.", "continuity_thread": proofContinuity,
		"source_entity_refs": []map[string]string{{"id": "proof_b"}},
	}); err == nil || p.b.lg.LastSeq() != before {
		t.Fatal("historical compatibility admitted an incomplete NEW synthesis")
	}
	model := &reflectionProofLLM{}
	facility := cognitive.NewSelfModel(s, model, selfModelCommitter{engine: engine})
	if err := facility.Execute(t.Context()); err != nil {
		t.Fatalf("next reflection failed: %v", err)
	}
	if model.calls != 1 || p.b.lg.LastSeq() != before+1 {
		t.Fatalf("reflection did not reach native commit: calls=%d seq=%d", model.calls, p.b.lg.LastSeq())
	}
	after, err := os.ReadFile(p.b.ledgerP)
	if err != nil || !bytes.HasPrefix(after, p.history) || len(after) <= len(p.history) {
		t.Fatalf("continuation changed prior signed history: %v", err)
	}
	if n, err := ledger.VerifyChain(p.b.ledgerP, p.b.kp.PublicKeyBytes(), nil); err != nil || n != 6 {
		t.Fatalf("continued history does not verify: %d, %v", n, err)
	}
	checkReflectionRuntime(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, filepath.Join(p.b.dir, "cold-rebuilt.db")} {
		t.Run(filepath.Base(target), func(t *testing.T) {
			reopened, err := store.NewForTest(target)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := reopened.ReplayFromFile(p.b.ledgerP); err != nil {
				t.Fatalf("continued reflection cannot replay at %s: %v", filepath.Base(target), err)
			}
			current, err := reopened.CurrentSelfModel()
			if err != nil || current == nil || current.ID != "proof_next" {
				t.Fatalf("successor lost after restart: %+v, %v", current, err)
			}
			p.checkPortrait(t, recallPortrait(t, reopened, "recorded experience"))
			if target == path {
				checkReflectionRuntime(t, reopened)
			}
		})
	}
}
