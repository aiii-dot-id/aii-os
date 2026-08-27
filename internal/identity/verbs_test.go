package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type testEventWriter struct {
	mu     sync.Mutex
	ledger *ledger.Ledger
	store  *store.Store
	key    *crypto.KeyPair
}

func (w *testEventWriter) Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	prepared, err := w.ledger.PreparePayload(payload)
	if modelID != "" {
		prepared, err = w.ledger.PreparePayloadWithModel(payload, modelID)
	}
	if err != nil {
		return nil, err
	}
	if err := w.store.ValidateEvent(eventType, ring, prepared.Bytes()); err != nil {
		return nil, err
	}
	evt, err := w.ledger.AppendPrepared(eventType, w.key.Fingerprint(), ring, prepared, w.key)
	if err != nil {
		return nil, err
	}
	return evt, w.store.Materialize(evt)
}

// setupEngine creates a full identity engine for testing.
func setupEngine(t *testing.T) (*Engine, *store.Store, *ledger.Ledger, *crypto.KeyPair, string) {
	t.Helper()
	dir := t.TempDir()

	kp, _ := crypto.GenerateKeyPair()
	keyPath := filepath.Join(dir, "identity.sec")
	_, _ = crypto.SaveKeyPair(kp, keyPath)

	lg, _ := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	st, _ := store.New(filepath.Join(dir, "aii.db"))

	// Birth
	evt, _ := lg.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "TestIdentity"}, kp)
	st.Materialize(evt)

	rings := ring.NewManager()
	toolReg := tools.NewRegistry(dir, nil, tools.Timeouts{})
	engine := NewEngine(st, &testEventWriter{ledger: lg, store: st, key: kp}, rings, discovererAdapter{toolReg})

	t.Cleanup(func() {
		lg.Close()
		st.Close()
	})

	return engine, st, lg, kp, dir
}

// R34: note is never friction-gated. Raw capture is immediate and unconditional.
func TestNoteNeverGated(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// Note should always succeed — no gate, no friction
	_, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "I noticed something"})
	if err != nil {
		t.Fatalf("note should never be gated (R34): %v", err)
	}

	// Experience should be in the store
	exps, _ := st.ListExperiences(10)
	if len(exps) == 0 {
		t.Error("note should create an experience")
	}

	// Should be raw (unprocessed)
	if exps[0].Raw != 1 {
		t.Error("note should create raw experience (R2: raw first, classify later)")
	}
}

// D-04: note mints experience.create, NOT conversation_turn
func TestNoteMintsExperienceCreate(t *testing.T) {
	engine, _, lg, _, _ := setupEngine(t)

	engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "test observation"})

	events, _ := ledger.ReadAll(lg.Path())
	found := false
	for _, evt := range events {
		if evt.Type == ledger.EventExperienceCreate {
			found = true
			break
		}
	}
	if !found {
		t.Error("note should mint experience.create (D-04), not conversation_turn")
	}
}

// R3: unknown commit variants fail closed
func TestCommitUnknownVariantFailsClosed(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "unknown.made.up"})
	if err == nil {
		t.Error("unknown commit variant should fail closed (R3)")
	}
}

// Ring 2 entry is gated on EVIDENCE (canon §11 Ring-2 threshold; R16:
// promotion is her conscious act THROUGH the gate — 2026-08-18 ring
// enforcement). An unconfirmed belief cannot become who-she-is; a
// confirmed one promotes by her conscious act.
func TestCommitRing2RequiresConfirmedStanding(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// An evidence-less belief: standing "new" → promote refused, typed.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "belief.upsert", "id": "b_unearned",
			"statement": "unsupported", "confidence": 0.5, "evidence_refs": "none",
		}); err != nil {
		t.Fatalf("belief.upsert setup: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b_unearned", "ring": 2}); err == nil {
		t.Fatal("promote without confirmed standing must be refused (Ring-2 evidence threshold)")
	}

	// Earn it: three sources, two classes → confirmed → promote mints.
	if err := engine.RecordConversationTurn("operator", "yes, that held up"); err != nil {
		t.Fatal(err)
	}
	opTurn, err := st.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("operator turn: %v %v", opTurn, err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "belief.upsert", "id": "b_earned",
			"statement": "earned", "confidence": 0.7, "evidence": "none",
		}); err != nil {
		t.Fatal(err)
	}
	for i, note := range []map[string]interface{}{
		{"content": "I watched it hold", "supports": "b_earned"},
		{"content": "the operator confirmed it", "source_turn": opTurn.TurnSeq, "supports": "b_earned"},
		{"content": "second observation of it holding", "supports": "b_earned"},
	} {
		if _, err := engine.ExecuteAction(context.Background(), "verb", "note", note); err != nil {
			t.Fatalf("note %d: %v", i, err)
		}
	}
	if got := st.StandingFor("b_earned"); got != "confirmed" {
		t.Fatalf("standing = %q, want confirmed", got)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b_earned", "ring": 2}); err != nil {
		t.Fatalf("confirmed belief must promote by her conscious act: %v", err)
	}
}

// Ring 0 is immutable — no writes after birth
func TestCommitRing0Fails(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":       "belief.upsert",
			"id":            "b1",
			"statement":     "test",
			"ring":          0,
			"confidence":    0.5,
			"evidence_refs": "e1",
		})
	if err == nil {
		t.Error("commit to Ring 0 should fail (immutable)")
	}
}

// Ring 4 is never minted — no ledger writes
func TestCommitRing4Fails(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":       "belief.upsert",
			"id":            "b1",
			"statement":     "test",
			"ring":          4,
			"confidence":    0.5,
			"evidence_refs": "e1",
		})
	if err == nil {
		t.Error("commit to Ring 4 should fail (never minted)")
	}
}

// Lesson 17 / R17: belief.upsert requires evidence_refs[] or evidence:none
func TestBeliefUpsertRequiresEvidence(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	// Without evidence_refs or evidence:none — should fail
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":    "belief.upsert",
			"id":         "b1",
			"statement":  "test belief",
			"ring":       3,
			"confidence": 0.8,
		})
	if err == nil {
		t.Error("belief.upsert should require evidence_refs[] (Lesson 17)")
	}

	// With evidence:none — should succeed
	_, err = engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":    "belief.upsert",
			"id":         "b1",
			"statement":  "test belief",
			"ring":       3,
			"confidence": 0.8,
			"evidence":   "none",
		})
	if err != nil {
		t.Errorf("belief.upsert with evidence:none should succeed: %v", err)
	}

	// With evidence_refs — should succeed
	_, err = engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":       "belief.upsert",
			"id":            "b2",
			"statement":     "tested belief",
			"ring":          3,
			"confidence":    0.9,
			"evidence_refs": "e1,e2,e3",
		})
	if err != nil {
		t.Errorf("belief.upsert with evidence_refs should succeed: %v", err)
	}
}

// R45: recall returns something (abstention is honest "nothing", not fabrication)
func TestRecallReturnsHonestResult(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// No beliefs yet — should return honest "Nothing to recall"
	result, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "beliefs"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}

	// Should not fabricate — should say "Nothing to recall" or list real beliefs
	if result == "" {
		t.Error("recall should return something (R45: honest abstention)")
	}

	// Add a belief and recall again
	if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
		VALUES ('b1', 'Testing is essential; unrelated ideas exist', 3, 0.9, 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}

	result, err = engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "testing"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}

	// Should contain the belief statement
	if !strings.Contains(result, "Testing is essential") {
		t.Error("recall should contain the belief statement")
	}

	result, err = engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "testing unrelated"})
	if err != nil {
		t.Fatalf("recall miss failed: %v", err)
	}
	if !strings.Contains(result, "literal substring") || !strings.Contains(result, "shorter distinctive word or phrase") {
		t.Errorf("recall miss lacks actionable literal-query guidance: %q", result)
	}
}

func TestRecallExcerptsRemainValidUTF8(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if err := st.AddConversationTurn("operator", strings.Repeat("🙂", 201)); err != nil {
		t.Fatal(err)
	}

	result, err := engine.ExecuteAction(context.Background(), "verb", "recall", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(result) {
		t.Fatal("recall returned invalid UTF-8")
	}
}

func TestRecallReportsUnavailableSource(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
		VALUES ('b1', 'Testing is essential', 3, 0.9, 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`DROP TABLE conversations`); err != nil {
		t.Fatal(err)
	}

	result, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "testing"})
	if err != nil {
		t.Fatalf("partial recall failed: %v", err)
	}
	if !strings.Contains(result, "Testing is essential") {
		t.Errorf("partial recall omitted available result: %q", result)
	}
	if !strings.Contains(result, "Unavailable sources: conversation:") {
		t.Errorf("partial recall concealed source failure: %q", result)
	}
}

func TestRecallFailsWhenAllSourcesAreUnavailable(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "testing"})
	if err == nil || !strings.Contains(err.Error(), "recall unavailable") {
		t.Fatalf("recall must fail when every source is unavailable: %v", err)
	}
}

// Tools: deny-list protects substrate
// (TestVerbToolsDenyList deleted with the engine's tool-execution case:
// the path had zero production callers, and substrate denial is pinned
// where it lives — internal/tools TestDenyListRead + TestPolicyIsTheEnforcer.)
func TestSendWritesToOutbox(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	engine.ExecuteAction(context.Background(), "verb", "send",
		map[string]interface{}{
			"to":      "operator",
			"message": "Hello operator",
		})

	msgs, _ := st.UndeliveredMessages()
	if len(msgs) == 0 {
		t.Error("send should write to outbox")
	}
	if msgs[0].Content != "Hello operator" {
		t.Errorf("outbox content = %q", msgs[0].Content)
	}
}

// work.update changes Ring 4 state
func TestWorkUpdateState(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// Start a work session
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action":      "start",
			"description": "Analyzing files",
		}); err != nil {
		t.Fatalf("work start failed: %v", err)
	}

	// Update state
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action": "update",
			"state":  "Read files A, B — C remains",
		}); err != nil {
		t.Fatalf("work update failed: %v", err)
	}

	ws, _ := st.ActiveWorkSession()
	if ws == nil {
		t.Fatal("expected active work session")
	}
	if ws.State != "Read files A, B — C remains" {
		t.Errorf("work state = %q, want Ring 4 state", ws.State)
	}
}

// Dawn's Charter #9: private experiences are not metabolized by facilities
func TestNotePrivateNotProcessed(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// Note with private=true
	engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{
			"content": "a private thought",
			"private": true,
		})

	exps, _ := st.ListExperiences(10)
	if len(exps) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(exps))
	}

	// Should be raw=0 (not processable by DREAM/CONSOLIDATE)
	if exps[0].Raw != 0 {
		t.Error("private experience should be raw=0 (not metabolizable) — Charter #9")
	}

	// Unprocessed count should be 0 (private doesn't count)
	count, _ := st.UnprocessedExperienceCount()
	if count != 0 {
		t.Errorf("private experience should not count as unprocessed, got %d", count)
	}
}

// Dawn's Charter #9: non-private experiences ARE metabolized
func TestNoteNonPrivateIsProcessed(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{
			"content": "a public observation",
		})

	exps, _ := st.ListExperiences(10)
	if len(exps) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(exps))
	}

	if exps[0].Raw != 1 {
		t.Error("non-private experience should be raw=1 (metabolizable)")
	}

	count, _ := st.UnprocessedExperienceCount()
	if count != 1 {
		t.Errorf("non-private experience should count as unprocessed, got %d", count)
	}
}

// (TestCommitBeliefPromoteSuspect deleted with the status lifecycle,
// 2026-08-17. The at-will promote stance was superseded 2026-08-18 by
// ring enforcement: Ring 2 requires confirmed standing — see
// TestCommitRing2RequiresConfirmedStanding for both directions. This
// test pins the refusal SHAPE and the explicit-ring guard.)
func TestCommitBeliefPromoteGates(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":    "belief.upsert",
			"id":         "b1",
			"statement":  "a working belief",
			"ring":       3,
			"confidence": 0.5,
			"evidence":   "none",
		})

	// Unconfirmed → typed refusal naming the requirement (R39 pattern).
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b1", "ring": 2})
	if err == nil {
		t.Fatal("unconfirmed promote must be refused (Ring-2 evidence threshold)")
	}
	if !strings.Contains(err.Error(), "confirmed") || !strings.Contains(err.Error(), "new") {
		t.Fatalf("refusal must name the requirement and the current standing: %v", err)
	}

	// Implicit ring (the ghost-promotion guard): still explicit-only.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b1"}); err == nil {
		t.Fatal("promote without explicit ring must fail closed")
	}
}

// R14: "exit verbs are exercised once on a real item before their listing
// ships." These tests are that exercise for the 8 new types.

func TestIntentionLifecycle(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":   "intention.create",
			"statement": "Understand the operator's working patterns",
			"why":       "condition 6: the relationship is load-bearing",
		}); err != nil {
		t.Fatalf("intention.create failed: %v", err)
	}

	ints, _ := st.ListIntentions()
	if len(ints) != 1 {
		t.Fatalf("expected 1 intention, got %d", len(ints))
	}
	id := ints[0].ID
	if ints[0].State != "active" {
		t.Errorf("new intention state = %q, want active", ints[0].State)
	}

	// THE YARDSTICK GATE (evaluate layer): a completion that states no
	// verdict is refused pre-mint — the live identity closed zero of
	// four intentions in six days while the outcome column sat
	// plumbed-but-dead, which is what an unenforced field does.
	for name, bad := range map[string]map[string]interface{}{
		"no outcome at all": {
			"variant": "intention.state_change", "id": id, "state": "completed"},
		"outcome without a verdict prefix": {
			"variant": "intention.state_change", "id": id, "state": "completed",
			"outcome": "operator prefers morning briefs with diffs only"},
		"verdict with nothing after it": {
			"variant": "intention.state_change", "id": id, "state": "completed",
			"outcome": "served:   "},
	} {
		_, err := engine.ExecuteAction(context.Background(), "verb", "commit", bad)
		if err == nil {
			t.Fatalf("%s: completion was minted without a yardstick", name)
		}
		if !strings.Contains(err.Error(), "served:") {
			t.Fatalf("%s: the refusal does not teach the form: %v", name, err)
		}
	}
	// Abandonment demands the same honesty.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "intention.state_change", "id": id, "state": "abandoned"}); err == nil {
		t.Fatal("abandonment was minted without a verdict")
	}

	// Complete it, with the verdict the gate demands.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "intention.state_change",
			"id":      id,
			"state":   "completed",
			"outcome": "served: operator prefers morning briefs with diffs only",
		}); err != nil {
		t.Fatalf("intention.state_change failed: %v", err)
	}

	ints, _ = st.ListIntentions()
	if len(ints) != 1 {
		t.Fatalf("completed intention is identity history — it must remain listed, got %d", len(ints))
	}
	if ints[0].State != "completed" {
		t.Errorf("completed intention state = %q, want completed", ints[0].State)
	}
	if ints[0].Outcome != "served: operator prefers morning briefs with diffs only" {
		t.Errorf("the verdict did not survive to the projection: %q", ints[0].Outcome)
	}
	if ints[0].ID != id {
		t.Errorf("wrong intention survived: %s", ints[0].ID)
	}
}

func TestCommitmentLifecycle(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// setupEngine births without a relationship — mint one for the counterpart
	if err := st.AddConversationTurn("operator", "Yes — rel_test approved."); err != nil {
		t.Fatal(err)
	}
	approval, err := st.GetLatestOperatorTurn()
	if err != nil || approval == nil {
		t.Fatalf("operator approval turn: %+v, %v", approval, err)
	}
	evt, err := lg.Append(ledger.EventRelationshipUpsert, kp.Fingerprint(), 1,
		map[string]interface{}{
			"id":                        "rel_test",
			"counterpart_name":          "Op",
			"counterpart_role":          "operator",
			"relationship_type":         "founding_operator",
			"operator_approval_excerpt": approval.Content,
			"operator_approval_turn":    approval.TurnSeq,
			"approval_basis":            "conversation_turn",
		}, kp)
	if err != nil {
		t.Fatalf("mint relationship: %v", err)
	}
	st.Materialize(evt)

	rel, err := st.FoundingRelationship()
	if err != nil || rel == nil {
		t.Fatalf("no founding relationship: %v", err)
	}

	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":        "commitment.promised",
			"description":    "Deliver the weekly summary by Friday",
			"counterpart_id": rel.ID,
		}); err != nil {
		t.Fatalf("commitment.promised failed: %v", err)
	}

	// Missing counterpart fails closed (Q1: the counterpart is what makes it a promise)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "commitment.promised", "description": "x"}); err == nil {
		t.Error("commitment.promised without counterpart_id should fail")
	}

	// The promised row exists — take its real ID (R14: exercise on a real item)
	comms, err := st.ListCommitments(false)
	if err != nil || len(comms) != 1 {
		t.Fatalf("expected 1 commitment, got %d (err %v)", len(comms), err)
	}
	if comms[0].State != "promised" {
		t.Errorf("new commitment state = %q, want promised", comms[0].State)
	}
	cid := comms[0].ID

	// Abandon with repair — the accountability dimension
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":      "commitment.state_change",
			"id":           cid,
			"state":        "abandoned",
			"repair_state": "reason: provider outage; apology sent; fix: retry schedule",
		}); err != nil {
		t.Fatalf("commitment.state_change failed on real item: %v", err)
	}
	comms, _ = st.ListCommitments(false)
	if comms[0].State != "abandoned" {
		t.Errorf("state after change = %q, want abandoned", comms[0].State)
	}
	if comms[0].RepairState == "" {
		t.Error("repair_state not persisted — accountability dimension lost")
	}
}

func TestWorkingStyleUpsert(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "working_style.upsert",
			"id":      "ws_1",
			"content": "The operator prefers terse diffs over prose explanations",
		}); err != nil {
		t.Fatalf("working_style.upsert failed: %v", err)
	}

	b, err := st.GetBelief("ws_1")
	if err != nil || b == nil {
		t.Fatalf("working style not in beliefs: %v", err)
	}
	if b.Statement != "The operator prefers terse diffs over prose explanations" {
		t.Errorf("statement = %q", b.Statement)
	}
}

func TestBeliefArchiveAndSupersede(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	// Create two beliefs
	for _, id := range []string{"b_old", "b_new"} {
		engine.ExecuteAction(context.Background(), "verb", "commit",
			map[string]interface{}{
				"variant":   "belief.upsert",
				"id":        id,
				"statement": "statement " + id,
				"evidence":  "none",
			})
	}

	// Archive b_old (R14 exercise)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.archive", "id": "b_old"}); err != nil {
		t.Fatalf("belief.archive failed: %v", err)
	}

	// Supersede b_new -> b_newer (R14 exercise)
	engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":   "belief.upsert",
			"id":        "b_newer",
			"statement": "refined view",
			"evidence":  "none",
		})
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "belief.supersede",
			"old_id":  "b_new",
			"new_id":  "b_newer",
			"reason":  "refined after new evidence",
		}); err != nil {
		t.Fatalf("belief.supersede failed: %v", err)
	}
}

func TestEdgeArchive(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	// Create belief + experience, then an edge
	engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.upsert", "id": "b1", "statement": "s", "evidence": "none"})
	engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{"content": "evidence text"})

	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":   "edge.create",
			"id":        "e1",
			"from_id":   "b1",
			"to_id":     "b1",
			"edge_type": "INTERPRETS",
		}); err != nil {
		t.Fatalf("edge.create failed: %v", err)
	}

	// Archive it (R14 exercise)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "edge.archive", "id": "e1"}); err != nil {
		t.Fatalf("edge.archive failed: %v", err)
	}
}

// R50: Ring 1 authority is the affirmative reply. The identity PROPOSES
// in conversation (citing the relationship id); the operator AFFIRMS the
// same id; the engine stamps the matched pair. No operator turn -> fail
// closed. Unpaired turns -> fail closed. Fabricated turn citations fail
// at materialization.
func TestRing1AffirmativeReplyModel(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// No operator turn yet -> Ring 1 proposal fails closed
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"id":                "rel_amendment_01",
			"counterpart_name":  "James",
			"relationship_type": "operator",
		})
	if err == nil {
		t.Fatal("Ring 1 without operator affirmative should fail closed")
	}

	// Unpaired operator 'yes' (no id reference, no resident proposal)
	if err := engine.RecordConversationTurn("operator", "Yes — approved, sounds good."); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"id":                "rel_amendment_01",
			"counterpart_name":  "James",
			"relationship_type": "operator",
		}); err == nil {
		t.Fatal("unpaired 'yes' must fail closed — the affirmation must cite the relationship id")
	}

	// The paired protocol: resident proposes citing the id...
	if err := engine.RecordConversationTurn("resident",
		"James, I'd like to record our working relationship as rel_amendment_01 — may I?"); err != nil {
		t.Fatal(err)
	}
	// ...operator affirms the same id
	if err := engine.RecordConversationTurn("operator",
		"Yes — rel_amendment_01 approved, promote the charter amendment."); err != nil {
		t.Fatal(err)
	}

	// Now the proposal mints with engine-stamped evidence
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"id":                "rel_amendment_01",
			"counterpart_name":  "James",
			"relationship_type": "operator",
			"trust_level":       "established",
		}); err != nil {
		t.Fatalf("Ring 1 with paired affirmative should mint: %v", err)
	}

	rel, err := st.FoundingRelationship()
	if err == nil && rel != nil && rel.ID == "rel_amendment_01" {
		// new operator row; founding still exists — fine, this is an amendment path test
	}
}

// H1 (2026-08-17 external review): the model chooses the needle for both
// haystacks. A low-entropy id ("e") made almost any operator message an
// "affirmation" and the model's own reply a "proposal"; a substring match
// let an id embedded in a longer token count. The pairing now requires a
// resident-minted rel_* id with an entropy floor, matched on token
// boundaries in BOTH turns.
func TestRing1IDEntropyAndBoundaries(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	// Give the transcript text that a low-entropy id would "match".
	if err := engine.RecordConversationTurn("resident", "Shall we record what we have?"); err != nil {
		t.Fatal(err)
	}
	if err := engine.RecordConversationTurn("operator", "yes, everything here sounds good"); err != nil {
		t.Fatal(err)
	}

	// (a) single-character id: matched both turns under Contains; now
	// refused at the format gate before any turn is consulted.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "e",
			"counterpart_name": "James", "relationship_type": "operator",
		}); err == nil {
		t.Fatal("single-character relationship id must be refused (H1)")
	}

	// (b) short rel_ id: below the entropy floor.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "rel_ok",
			"counterpart_name": "James", "relationship_type": "operator",
		}); err == nil {
		t.Fatal("below-floor relationship id must be refused (H1)")
	}

	// (c) embedded token: the id appears only inside a longer token in the
	// operator's turn — Contains matched it, boundaries must not.
	if err := engine.RecordConversationTurn("resident",
		"I'd like to record our relationship as rel_deep_trust_a1 — may I?"); err != nil {
		t.Fatal(err)
	}
	if err := engine.RecordConversationTurn("operator",
		"hmm, rel_deep_trust_a1x maybe? let me think"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "rel_deep_trust_a1",
			"counterpart_name": "James", "relationship_type": "operator",
		}); err == nil {
		t.Fatal("id embedded in a longer token is not an affirmation (H1)")
	}

	// (d) the honest pair still mints.
	if err := engine.RecordConversationTurn("resident",
		"Proposing again: rel_deep_trust_a1 — may I record it?"); err != nil {
		t.Fatal(err)
	}
	if err := engine.RecordConversationTurn("operator",
		"Yes — rel_deep_trust_a1 approved."); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "rel_deep_trust_a1",
			"counterpart_name": "James", "relationship_type": "operator",
		}); err != nil {
		t.Fatalf("boundary-exact paired affirmation must mint: %v", err)
	}
}

// The pairing halves: affirmation citing the id WITHOUT a preceding
// resident proposal fails closed — a resident cannot stamp a proposal the
// operator answered into evidence for something the resident never raised.
func TestRing1PairingRequiresProposal(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	_ = st

	// Operator mentions the id, but no resident proposal precedes it
	if err := engine.RecordConversationTurn("operator", "rel_ghost_claim1 is fine by me"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"id":                "rel_ghost_claim1",
			"counterpart_name":  "James",
			"relationship_type": "operator",
		}); err == nil {
		t.Fatal("operator id-citation without a resident proposal must fail closed")
	}
}

// Fabricated approval citations fail at materialization
func TestRing1FabricatedTurnFailsClosed(t *testing.T) {
	engine, _, lg, kp, _ := setupEngine(t)
	_ = engine

	// No conversations at all — cite turn 42
	evt, _ := lg.Append(ledger.EventRelationshipUpsert, kp.Fingerprint(), 1,
		map[string]interface{}{
			"id":                        "rel_fake",
			"counterpart_name":          "Nobody",
			"relationship_type":         "operator",
			"operator_approval_excerpt": "I totally approved this",
			"operator_approval_turn":    42,
		}, kp)
	// Materialize against a fresh store so the ledger-mirror FK holds
	st2, _ := store.New(filepath.Join(t.TempDir(), "verify.db"))
	defer st2.Close()
	if err := st2.Materialize(evt); err == nil {
		t.Fatal("fabricated turn citation should fail closed at materialization")
	}
}

// Regression (honesty review A7): the model must not be able to reach the
// removed birth-form approval basis — the engine stamps conversation_turn
// over any model-supplied value, and a raw event claiming the removed basis
// fails closed at materialization.
func TestApprovalBasisCannotBeForged(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// Ensure a PAIRED exchange exists so the verb path proceeds
	st.AddConversationTurn("resident", "Operator, shall I record our relationship as rel_forge_basis?")
	st.AddConversationTurn("operator", "yes, rel_forge_basis approved — I vouch for this relationship")

	// Model tries to claim the birth basis + fabricate a turn stamp. The
	// engine overrides BOTH with engine-stamped values (real latest turn,
	// basis=conversation_turn), so the commit SUCCEEDS — but with the
	// honest evidence, never the model's. Assert the override landed.
	result, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":                   "relationship.upsert",
		"id":                        "rel_forge_basis",
		"counterpart_name":          "Op",
		"relationship_type":         "operator",
		"operator_approval_excerpt": "totally approved",
		"operator_approval_turn":    999,              // fabricated — engine overrides with the real latest turn
		"approval_basis":            "firstboot_form", // the attack — engine must override
	})
	if err != nil {
		t.Fatalf("commit with operator turn on record should succeed: %v", err)
	}
	_ = result
	events, rerr := ledger.ReadAll(lg.Path())
	if rerr != nil {
		t.Fatalf("read ledger: %v", rerr)
	}
	var minted *ledger.Event
	for i := range events {
		if events[i].Type == ledger.EventRelationshipUpsert {
			e := events[i]
			if e.Payload != nil && strings.Contains(string(e.Payload), "rel_forge_basis") {
				minted = &e
			}
		}
	}
	if minted == nil {
		t.Fatal("relationship event not found in ledger")
	}
	var p struct {
		Basis   string `json:"approval_basis"`
		Turn    uint64 `json:"operator_approval_turn"`
		Excerpt string `json:"operator_approval_excerpt"`
	}
	if err := json.Unmarshal(minted.Payload, &p); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if p.Basis != "conversation_turn" {
		t.Fatalf("approval_basis must be engine-stamped conversation_turn, got %q", p.Basis)
	}
	if p.Turn == 999 {
		t.Fatal("fabricated turn 999 survived — engine must override with the real turn")
	}
	if p.Excerpt == "totally approved" {
		t.Fatal("model-supplied excerpt survived — engine must stamp the real operator turn")
	}

	// No founding exception exists: a raw founding event claiming the
	// removed form basis fails closed at materialization.
	evt, _ := lg.Append(ledger.EventRelationshipUpsert, kp.Fingerprint(), 1,
		map[string]interface{}{
			"id":                        "rel_forge2",
			"counterpart_name":          "Nobody",
			"counterpart_role":          "operator",
			"relationship_type":         "founding_operator",
			"operator_approval_excerpt": "I approved this",
			"approval_basis":            "firstboot_form",
		}, kp)
	st2, _ := store.New(filepath.Join(t.TempDir(), "forge2.db"))
	defer st2.Close()
	if err := st2.Materialize(evt); err == nil {
		t.Fatal("firstboot_form basis must fail closed, including for a founding relationship")
	}
}

// Regression (honesty review B1): edges to nonexistent entities are
// refused at every mint path and the refusal is REPORTED — never silently
// swallowed. Ghost edges certify nothing.
func TestGhostEdgesRefusedAndReported(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// Create a real belief to target
	evt, err := lg.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3, map[string]interface{}{
		"id": "bel_real", "statement": "real belief", "ring": 3, "confidence": 0.5,
	}, kp)
	if err != nil {
		t.Fatal(err)
	}
	st.Materialize(evt)

	// note(supports=ghost) — note mints, edge refused, refusal IN THE RESULT
	result, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
		"content":  "an observation",
		"supports": "bel_ghost",
	})
	if err != nil {
		t.Fatalf("note itself must succeed (R2: capture never gated): %v", err)
	}
	if !strings.Contains(result, "Edge refusals") || !strings.Contains(result, "bel_ghost") {
		t.Fatalf("ghost-edge refusal must be reported in the result, got: %q", result)
	}

	// commit edge.create with a ghost endpoint: refused fail-closed
	_, err = engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":   "edge.create",
		"edge_type": "SUPPORTS",
		"from_id":   "exp_ghost",
		"to_id":     "bel_real",
	})
	if err == nil || !strings.Contains(err.Error(), "no such entity") {
		t.Fatalf("edge.create with ghost endpoint must be refused, got: %v", err)
	}

	// belief.upsert with ghost evidence: belief mints, refusal reported
	result, err = engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":       "belief.upsert",
		"id":            "bel_new",
		"statement":     "new belief from evidence",
		"evidence_refs": "exp_ghost1,exp_ghost2", // R17 gate key
		"evidence":      "exp_ghost1,exp_ghost2", // mint path key
	})
	if err != nil {
		t.Fatalf("belief.upsert itself must succeed: %v", err)
	}
	if !strings.Contains(result, "Edge refusals") {
		t.Fatalf("ghost evidence refusals must be reported, got: %q", result)
	}
}

// discovererAdapter widens the registry's Discover to the domain type.
type discovererAdapter struct{ reg *tools.Registry }

func (d discovererAdapter) Discover(depth int) []ToolInfo {
	infos := d.reg.Discover(depth)
	out := make([]ToolInfo, 0, len(infos))
	for _, i := range infos {
		out = append(out, ToolInfo{Name: i.Name, Description: i.Description})
	}
	return out
}

// A delivery is NEVER identity truth (James's ruling 2026-08-17,
// extending the 2026-08-16 correction): work state is Ring 4 ephemeral,
// and even a delivery that names a commitment does not complete it —
// whether a promise is KEPT is the resident's conscious act
// (commit commitment.state_change), never the substrate's conclusion.
func TestDeliverCommitmentPathOnly(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "start", "description": "the weekly summary",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Plain deliver: no ledger event, no experience
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "deliver", "result": "summary attached",
	}); err != nil {
		t.Fatal(err)
	}
	for _, evt := range mustReadLedger(t, lg) {
		if evt.Type == ledger.EventExperienceCreate || evt.Type == ledger.EventCommitmentStateChange {
			var p map[string]interface{}
			json.Unmarshal(evt.Payload, &p)
			if strings.Contains(fmt.Sprint(p), "summary") {
				t.Fatal("plain deliver must NOT mint identity truth — work output is Ring 4; the resident notes it or completes the promise consciously")
			}
		}
	}

	// 2. Deliver ON a commitment: still NO mint — the delivery lands in
	// Ring 4, the completion belongs to the resident's commit act.
	// (commitments FK-reference relationships — mint the founding one first)
	relEvt, err := lg.Append(ledger.EventRelationshipUpsert, kp.Fingerprint(), 1, map[string]interface{}{
		"id": "rel_test", "counterpart_name": "Peer", "counterpart_role": "peer",
		"relationship_type": "peer", "charter_text": "c",
	}, kp)
	if err != nil {
		t.Fatal(err)
	}
	st.Materialize(relEvt)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant": "commitment.promised", "id": "cm1", "description": "weekly summary by Friday", "counterpart_id": "rel_test",
	}); err != nil {
		t.Fatalf("commitment.promised: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "start", "description": "the weekly summary (again)",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "deliver", "result": "summary attached", "commitment_id": "cm1",
	})
	if err != nil {
		t.Fatalf("deliver on commitment: %v", err)
	}
	if !strings.Contains(res, "commit commitment.state_change") {
		t.Fatalf("deliver must TEACH the conscious completion path, got: %s", res)
	}
	// No commitment.state_change may have been minted by the delivery.
	for _, evt := range mustReadLedger(t, lg) {
		if evt.Type == ledger.EventCommitmentStateChange {
			t.Fatal("deliver on a commitment minted commitment.state_change — completion is the resident's act, not the substrate's")
		}
	}
	// 3. The conscious path completes it: commit commitment.state_change.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant": "commitment.state_change", "id": "cm1", "state": "completed", "result": "summary attached",
	}); err != nil {
		t.Fatalf("conscious completion: %v", err)
	}
	cms, _ := st.ListCommitments(false) // all states: completed included
	var completed bool
	for _, c := range cms {
		if c.ID == "cm1" && c.Result == "summary attached" {
			completed = true
		}
	}
	if !completed {
		t.Fatal("conscious commitment completion must materialize with the delivered result")
	}
}

// (TestDeliverMintsExperience deleted with the corrected deliver contract:
// a delivery is NOT substrate-minted identity truth. See
// TestDeliverCommitmentPathOnly — work output enters the ledger only as a
// fulfilled commitment or the resident's own note.)
//
// (TestBeliefConfirmStampsOperatorEvidence deleted with belief.confirm,
// 2026-08-17 ruling: hallucinated YAPB. An identity promotes any belief
// RING3→RING2 at will; THE GATE IS EVIDENCE. There is no operator
// affirmation verb for the resident's epistemics — the operator's role
// is Ring 1. Operator-class evidence, when it exists, must arrive as
// honest evidence (verified provenance on real testimony), not as a
// permission path.)
func TestBeliefConfirmIsGone(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant": "belief.confirm", "id": "b_any",
	}); err == nil {
		t.Fatal("belief.confirm must not exist — the gate is evidence, not operator ceremony")
	}
}

func mustReadLedger(t *testing.T, lg *ledger.Ledger) []ledger.Event {
	t.Helper()
	events, err := ledger.ReadAll(lg.Path())
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// R1 pass-through cursor: the footer promises full enumeration — deep
// memory must actually be page-reachable, and experience provenance must
// be visible (the resident can see which evidence is operator-class).
func TestRecallCursorPagesDeepMemory(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// 25 experiences (page cap is 20) + one operator-class
	for i := 0; i < 25; i++ {
		evt, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": fmt.Sprintf("deep%d", i), "content": fmt.Sprintf("deep memory %d", i), "category": "observation"}, kp)
		st.Materialize(evt)
	}
	// Operator-class evidence carries its citation (H3): a real turn.
	if err := st.AddConversationTurn("operator", "yes, I confirm the archive sweep"); err != nil {
		t.Fatal(err)
	}
	opTurn, err := st.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("operator turn: %v %v", opTurn, err)
	}
	opEvt, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "op_ev", "content": "operator affirmation turn 3", "category": "observation", "provenance": "operator", "source_turn": opTurn.TurnSeq}, kp)
	if err := st.Materialize(opEvt); err != nil {
		t.Fatal(err)
	}

	r1, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(r1, "] deep memory"); got != 19 {
		t.Fatalf("first page shows the 20 newest (19 deep + 1 operator), got %d deep", got)
	}
	if !strings.Contains(r1, "op_ev, operator]") {
		t.Fatal("experience lines must carry provenance — operator-class evidence must be visible as such")
	}

	// Page 2 with the REAL lowest created_seq shown (the footer's rule).
	shown, _ := st.ListExperiences(20) // same page: newest-first
	lowest := shown[len(shown)-1].CreatedSeq
	r2, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{
		"after_seq": float64(lowest),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(r2, "] deep memory"); got != 6 {
		t.Fatalf("page 2 must reach the remaining 6 experiences (operator one was in page 1), got %d", got)
	}
}

// The organs are visible to the identity (2026-08-17 ruling): the tools
// verb lists ORGANS FIRST — before this, it listed only physical tools
// and commit/work were discoverable nowhere resident-facing.
func TestToolsVerbListsOrgansFirst(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	res, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	// Organs section present and leading (before sandbox tools).
	if oi, ti := strings.Index(res, "Your organs"), strings.Index(res, "Tools in your sandbox"); oi < 0 || (ti >= 0 && oi > ti) {
		t.Fatalf("organs must lead the tools listing")
	}
	for _, organ := range []string{"note", "recall", "timer", "send", "work", "commit", "tools"} {
		if !strings.Contains(res, organ+" —") {
			t.Fatalf("organ %q must be listed", organ)
		}
	}
	// commit is described — the resident learns self-authorship exists.
	if !strings.Contains(res, "signed ledger") {
		t.Fatal("commit's description must teach what it is")
	}
}

// H3 (2026-08-17 external review): the R16 ladder was unreachable — no
// non-test path ever wrote operator/external provenance, so
// authorshipClasses() always returned 1 and confirmed/trusted were dead
// code. This is the ladder's first real climb: three sources spanning
// three classes, each earned through a verified citation, derive
// "confirmed" — and the fabricated citations all fail closed.
func TestBeliefLadderReachesConfirmed(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// The operator says something worth believing.
	if err := engine.RecordConversationTurn("operator",
		"The witness anchor held through the whole outage."); err != nil {
		t.Fatal(err)
	}
	opTurn, err := st.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("operator turn: %v %v", opTurn, err)
	}

	// The world got fetched (wired from web_fetch by the app; direct here).
	engine.NoteExternalFetch("https://status.example.org/incident-7")

	// The belief, then three supporting sources in three classes.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant": "belief.upsert", "id": "b_anchor_holds",
		"statement": "the witness anchor survives outages", "evidence": "none",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
		"content":  "I watched the anchor sequence complete after the network came back",
		"supports": "b_anchor_holds",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
		"content":     "James said the anchor held through the outage",
		"source_turn": opTurn.TurnSeq, "supports": "b_anchor_holds",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
		"content":    "the status page confirms the anchor window closed cleanly",
		"source_url": "https://status.example.org/incident-7", "supports": "b_anchor_holds",
	}); err != nil {
		t.Fatal(err)
	}

	if got := st.StandingFor("b_anchor_holds"); got != "confirmed" {
		t.Fatalf("three sources across three classes must derive confirmed, got %q", got)
	}

	// Fabrications fail closed.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
		"content": "operator agreed", "source_turn": 9999,
	}); err == nil {
		t.Fatal("fabricated source_turn must be refused")
	}
	if err := engine.RecordConversationTurn("resident", "I think it held"); err != nil {
		t.Fatal(err)
	}
	var resTurn uint64
	if turn, _ := st.GetTurnBySeq(opTurn.TurnSeq + 1); turn != nil && turn.Role == "resident" {
		resTurn = turn.TurnSeq
	}
	if resTurn > 0 {
		if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
			"content": "the operator confirmed it", "source_turn": resTurn,
		}); err == nil {
			t.Fatal("resident turn cited as operator testimony must be refused")
		}
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
		"content": "the docs say so", "source_url": "https://never-fetched.example.org/",
	}); err == nil {
		t.Fatal("unfetched source_url must be refused")
	}
}

// Sub-agent spawn gates (2026-08-18 agency ruling): ceilings live in
// config; depth is engine-stamped from context, never model-claimed;
// refusals are typed and name the ceiling.
func TestWorkSpawnGates(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	engine.SetAgencyLimits(2, 1, 20, 600) // depth ≤ 2, one live sub-agent

	// Model-claimed depth is overridden by the engine stamp (no ctx
	// value = depth 0): the spawn succeeds at depth 1.
	out, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "measure the anchor", "_subagent_depth": 99})
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if !strings.Contains(out, "depth 1") {
		t.Fatalf("engine must stamp depth (model claimed 99): %s", out)
	}
	var raw, dedup string
	if err := st.DB().QueryRow(
		`SELECT payload, dedup_key FROM work_queue WHERE kind = ?`, SubagentWorkKind,
	).Scan(&raw, &dedup); err != nil {
		t.Fatal(err)
	}
	var request SubagentRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatalf("durable request did not decode: %v", err)
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("durable request is invalid: %v", err)
	}
	if request.SessionID != dedup || request.Goal != "measure the anchor" || request.Depth != 1 {
		t.Fatalf("durable request = %+v, dedup=%q", request, dedup)
	}

	// Parallel ceiling: one live item → second spawn refused, typed.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "another"}); err == nil ||
		!strings.Contains(err.Error(), "max_parallel_subagents") {
		t.Fatalf("parallel ceiling must refuse with its name: %v", err)
	}

	// Depth ceiling: context says we're already at depth 2 → refused.
	ctx := context.WithValue(context.Background(), SubagentDepth{}, 2)
	if _, err := engine.ExecuteAction(ctx, "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "too deep"}); err == nil ||
		!strings.Contains(err.Error(), "max_subagent_depth") {
		t.Fatalf("depth ceiling must refuse with its name: %v", err)
	}

	// Disabled (0): honest refusal.
	engine.SetAgencyLimits(0, 3, 20, 600)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "x"}); err == nil ||
		!strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled spawning must refuse honestly: %v", err)
	}

	// The session is Ring 4 ephemeral: it exists in the store, and the
	// ledger got NOTHING (a sub-goal is not identity truth).
	subs, err := st.CountLiveWork("subagent.run")
	if err != nil || subs != 1 {
		t.Fatalf("one live sub-agent item expected: %d %v", subs, err)
	}
}

func TestWorkActionsHonorBoundSession(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if err := st.StartWorkSession("ws_older", "older"); err != nil {
		t.Fatal(err)
	}
	if err := st.StartWorkSession("ws_newer", "newer"); err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), SubagentWorkSession{}, "ws_older")
	if _, err := engine.ExecuteAction(ctx, "verb", "work",
		map[string]interface{}{"action": "update", "state": "older state"}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "older result"}); err != nil {
		t.Fatal(err)
	}

	var olderStatus, olderState, olderResult string
	if err := st.DB().QueryRow(
		`SELECT status, state, result FROM work_sessions WHERE id = ?`, "ws_older",
	).Scan(&olderStatus, &olderState, &olderResult); err != nil {
		t.Fatal(err)
	}
	if olderStatus != "delivered" || olderState != "older state" || olderResult != "older result" {
		t.Fatalf("bound session = (%q, %q, %q)", olderStatus, olderState, olderResult)
	}
	var newerStatus, newerState, newerResult string
	if err := st.DB().QueryRow(
		`SELECT status, COALESCE(state,''), COALESCE(result,'') FROM work_sessions WHERE id = ?`, "ws_newer",
	).Scan(&newerStatus, &newerState, &newerResult); err != nil {
		t.Fatal(err)
	}
	if newerStatus != "active" || newerState != "" || newerResult != "" {
		t.Fatalf("unbound session was crossed: (%q, %q, %q)", newerStatus, newerState, newerResult)
	}
}

// R60: the duplicate pushback is a mirror with an override — one
// bounce, then the identity's deliberate act always succeeds; and a
// spawned run's mint envelope is flat, typed, and leaves the outcome
// path open.
func TestDuplicatePushbackAndMintEnvelope(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.SetAgencyLimits(3, 3, 2, 600) // envelope of 2 for the test

	if _, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the anchor held"}); err != nil {
		t.Fatal(err)
	}
	// Exact repeat → pushback naming the earlier experience.
	_, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the anchor held"})
	if err == nil || !strings.Contains(err.Error(), "noticed exactly this before") {
		t.Fatalf("duplicate must bounce with the mirror: %v", err)
	}
	// Deliberate override always succeeds.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the anchor held", "duplicate_ok": true}); err != nil {
		t.Fatalf("override must mint: %v", err)
	}

	// Belief statement duplicate under a different id: bounce + override.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.upsert", "id": "b_one", "statement": "anchors hold", "evidence": "none"}); err != nil {
		t.Fatal(err)
	}
	_, err = engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.upsert", "id": "b_two", "statement": "anchors hold", "evidence": "none"})
	if err == nil || !strings.Contains(err.Error(), "already states exactly this") {
		t.Fatalf("duplicate statement must bounce: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.upsert", "id": "b_two", "statement": "anchors hold", "evidence": "none", "duplicate_ok": true}); err != nil {
		t.Fatalf("belief override must mint: %v", err)
	}

	// Mint envelope: only counted in sub-agent context; flat and typed.
	mints := 0
	ctx := context.WithValue(context.Background(), SubagentMints{}, &mints)
	for i := 0; i < 2; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note",
			map[string]interface{}{"content": fmt.Sprintf("sub observation %d", i)}); err != nil {
			t.Fatalf("mint %d inside envelope: %v", i, err)
		}
	}
	_, err = engine.ExecuteAction(ctx, "verb", "note",
		map[string]interface{}{"content": "one too many"})
	if err == nil || !strings.Contains(err.Error(), "mint envelope reached") {
		t.Fatalf("envelope must refuse, typed: %v", err)
	}
	// The main thread is untouched by the envelope.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the narrator still notices freely"}); err != nil {
		t.Fatalf("main-thread capture must stay frictionless: %v", err)
	}
}
