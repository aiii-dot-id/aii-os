package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
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

// .
func setupEngine(t *testing.T) (*Engine, *store.Store, *ledger.Ledger, *crypto.KeyPair, string) {
	t.Helper()
	dir := t.TempDir()

	kp, _ := crypto.GenerateKeyPair()
	keyPath := filepath.Join(dir, "identity.sec")
	_, _ = crypto.SaveKeyPair(kp, keyPath)

	lg, _ := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	st, _ := store.New(filepath.Join(dir, "aii.db"))

	// .
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

// .
func TestNoteNeverGated(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// .
	_, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "I noticed something"})
	if err != nil {
		t.Fatalf("note should never be gated: %v", err)
	}

	// .
	exps, _ := st.ListExperiences(10)
	if len(exps) == 0 {
		t.Error("note should create an experience")
	}

	// .
	if exps[0].Raw != 1 {
		t.Error("note should create raw experience")
	}
}

// .
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

// .
func TestCommitUnknownVariantFailsClosed(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "unknown.made.up"})
	if err == nil {
		t.Error("unknown commit variant should fail closed")
	}
}

// .
// .
// .
// .
func TestCommitRing2RequiresConfirmedStanding(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// .
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

	// .
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
	if got := mustStanding(t, st, "b_earned"); got != "confirmed" {
		t.Fatalf("standing = %q, want confirmed", got)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b_earned", "ring": 3}); err == nil {
		t.Fatal("promote at ring 3 must be refused — promote is owner-fixed at 2")
	} else if !strings.Contains(err.Error(), "owner-derived") {
		t.Fatalf("refusal must be the R3 owner-derived check, got: %v", err)
	}

	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b_earned", "ring": 2}); err != nil {
		t.Fatalf("confirmed belief must promote by her conscious act: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
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
		t.Fatal("commit naming Ring 0 must fail")
	}
	if !strings.Contains(err.Error(), "owner-derived") {
		t.Errorf("refusal must be the R3 owner-derived check, got: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
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
		t.Fatal("commit naming Ring 4 must fail")
	}
	if !strings.Contains(err.Error(), "owner-derived") {
		t.Errorf("refusal must be the R3 owner-derived check, got: %v", err)
	}
}

// .
func TestBeliefUpsertRequiresEvidence(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	// .
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

	// .
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

	// .
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

// .
func TestRecallReturnsHonestResult(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// .
	result, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "beliefs"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}

	// .
	if result == "" {
		t.Error("recall should return something")
	}

	// .
	if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
		VALUES ('b1', 'Testing is essential; unrelated ideas exist', 3, 0.9, 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}

	result, err = engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "testing"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}

	// .
	if !strings.Contains(result, "Testing is essential") {
		t.Error("recall should contain the belief statement")
	}

	// .
	// .
	result, err = engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "testing unrelated"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if !strings.Contains(result, "Testing is essential") || !strings.Contains(result, "rank 1,") || !strings.Contains(result, "strength") {
		t.Errorf("a two-word query must reach the belief holding both words and disclose its rank and strength: %q", result)
	}

	result, err = engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "zebra"})
	if err != nil {
		t.Fatalf("recall miss failed: %v", err)
	}
	if !strings.Contains(result, "No exact-word or fuzzy match") || !strings.Contains(result, "distinctive word") {
		t.Errorf("recall miss lacks actionable guidance: %q", result)
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

// .
// .
// .
// .
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

// .
func TestWorkUpdateState(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action":      "start",
			"description": "Analyzing files",
		}); err != nil {
		t.Fatalf("work start failed: %v", err)
	}

	// .
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

// .
func TestNotePrivateNotProcessed(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// .
	engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{
			"content": "a private thought",
			"private": true,
		})

	exps, _ := st.ListExperiences(10)
	if len(exps) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(exps))
	}

	// .
	if exps[0].Raw != 0 {
		t.Error("private experience should be raw=0 (not metabolizable) — Charter #9")
	}

	// .
	count, _ := st.UnprocessedExperienceCount()
	if count != 0 {
		t.Errorf("private experience should not count as unprocessed, got %d", count)
	}
}

// .
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

// .
// .
// .
// .
// .
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

	// .
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b1", "ring": 2})
	if err == nil {
		t.Fatal("unconfirmed promote must be refused (Ring-2 evidence threshold)")
	}
	if !strings.Contains(err.Error(), "confirmed") || !strings.Contains(err.Error(), "new") {
		t.Fatalf("refusal must name the requirement and the current standing: %v", err)
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b1"}); err == nil {
		t.Fatal("promote without explicit ring must fail closed")
	}
}

// .
// .

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

	// .
	// .
	// .
	// .
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
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "intention.state_change", "id": id, "state": "abandoned"}); err == nil {
		t.Fatal("abandonment was minted without a verdict")
	}

	// .
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

	// .
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

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "commitment.promised", "description": "x"}); err == nil {
		t.Error("commitment.promised without counterpart_id should fail")
	}

	// .
	comms, err := st.ListCommitments(false)
	if err != nil || len(comms) != 1 {
		t.Fatalf("expected 1 commitment, got %d (err %v)", len(comms), err)
	}
	if comms[0].State != "promised" {
		t.Errorf("new commitment state = %q, want promised", comms[0].State)
	}
	cid := comms[0].ID

	// .
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
	engine, st, lg, kp, _ := setupEngine(t)

	// .
	// .
	evt, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
		"id": "exp_terse", "content": "asked for the diff, not the essay", "category": "observation",
	}, kp)
	if err != nil {
		t.Fatal(err)
	}
	st.Materialize(evt)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "working_style.upsert",
			"id":      "ws_1",
			"content": "The operator prefers terse diffs over prose explanations",
		}); err == nil {
		t.Fatal("a working style with no evidence declared was minted — a belief from nowhere")
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":       "working_style.upsert",
			"id":            "ws_1",
			"content":       "The operator prefers terse diffs over prose explanations",
			"evidence_refs": []interface{}{"exp_terse"},
		}); err != nil {
		t.Fatalf("working_style.upsert failed: %v", err)
	}
	edges, err := st.ListEdgesForBelief("ws_1")
	if err != nil || len(edges) != 1 || edges[0].FromID != "exp_terse" {
		t.Fatalf("the working style's evidence edge was not minted: %v %v", edges, err)
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

	// .
	for _, id := range []string{"b_old", "b_new"} {
		engine.ExecuteAction(context.Background(), "verb", "commit",
			map[string]interface{}{
				"variant":   "belief.upsert",
				"id":        id,
				"statement": "statement " + id,
				"evidence":  "none",
			})
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.archive", "id": "b_old"}); err != nil {
		t.Fatalf("belief.archive failed: %v", err)
	}

	// .
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

	// .
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

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "edge.archive", "id": "e1"}); err != nil {
		t.Fatalf("edge.archive failed: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
func TestRing1AffirmativeReplyModel(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	// .
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"charter_text":      "Sam is my operator; this is the working relationship.",
			"id":                "rel_amendment_01",
			"counterpart_name":  "Sam",
			"relationship_type": "operator",
		})
	if err == nil {
		t.Fatal("Ring 1 without operator affirmative should fail closed")
	}

	// .
	// .
	// .
	if err := engine.RecordConversationTurn("operator", "Yes — approved, sounds good."); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"charter_text":      "Sam is my operator; this is the working relationship.",
			"id":                "rel_amendment_01",
			"counterpart_name":  "Sam",
			"relationship_type": "operator",
		}); err != nil {
		t.Fatalf("warm unpaired affirmative must mint under %v", err)
	}

	// .
	if err := engine.RecordConversationTurn("resident",
		"Sam, I'd like to record our working relationship as rel_amendment_01 — may I?"); err != nil {
		t.Fatal(err)
	}
	// .
	if err := engine.RecordConversationTurn("operator",
		"Yes — rel_amendment_01 approved, promote the charter amendment."); err != nil {
		t.Fatal(err)
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"charter_text":      "Sam is my operator; this is the working relationship.",
			"id":                "rel_amendment_01",
			"counterpart_name":  "Sam",
			"relationship_type": "operator",
			"trust_level":       "established",
		}); err != nil {
		t.Fatalf("Ring 1 with paired affirmative should mint: %v", err)
	}

	// .
	// .
	// .
	// .
	// .
}

// .
// .
// .
// .
func TestRing1IDEntropyAndBoundaries(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	// .
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "e",
			"charter_text":     "Sam is my operator; this is the working relationship.",
			"counterpart_name": "Sam", "relationship_type": "operator",
		}); err == nil {
		t.Fatal("single-character relationship id must be refused (H1)")
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "relationship.upsert", "id": "rel_ok",
			"charter_text":     "Sam is my operator; this is the working relationship.",
			"counterpart_name": "Sam", "relationship_type": "operator",
		}); err == nil {
		t.Fatal("below-floor relationship id must be refused (H1)")
	}

	// .
	// .
	// .
}

// .
// .
// .
func TestRing1AffirmativeWithoutProposalMints(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	_ = st

	if err := engine.RecordConversationTurn("operator", "rel_ghost_claim1 is fine by me"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant":           "relationship.upsert",
			"charter_text":      "Sam is my operator; this is the working relationship.",
			"id":                "rel_ghost_claim1",
			"counterpart_name":  "Sam",
			"relationship_type": "operator",
		}); err != nil {
		t.Fatalf("operator affirmative alone must mint under %v", err)
	}
}

// .
func TestRing1FabricatedTurnFailsClosed(t *testing.T) {
	engine, _, lg, kp, _ := setupEngine(t)
	_ = engine

	// .
	evt, _ := lg.Append(ledger.EventRelationshipUpsert, kp.Fingerprint(), 1,
		map[string]interface{}{
			"id":                        "rel_fake",
			"counterpart_name":          "Nobody",
			"relationship_type":         "operator",
			"operator_approval_excerpt": "I totally approved this",
			"operator_approval_turn":    42,
		}, kp)
	// .
	st2, _ := store.New(filepath.Join(t.TempDir(), "verify.db"))
	defer st2.Close()
	if err := st2.Materialize(evt); err == nil {
		t.Fatal("fabricated turn citation should fail closed at materialization")
	}
}

// .
// .
// .
// .
func TestApprovalBasisCannotBeForged(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// .
	st.AddConversationTurn("resident", "Operator, shall I record our relationship as rel_forge_basis?")
	st.AddConversationTurn("operator", "yes, rel_forge_basis approved — I vouch for this relationship")

	// .
	// .
	// .
	// .
	result, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":                   "relationship.upsert",
		"charter_text":              "Sam is my operator; this is the working relationship.",
		"id":                        "rel_forge_basis",
		"counterpart_name":          "Op",
		"relationship_type":         "operator",
		"operator_approval_excerpt": "totally approved",
		"operator_approval_turn":    999,
		"approval_basis":            "firstboot_form",
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

	// .
	// .
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

// .
// .
// .
func TestGhostEdgesRefusedAndReported(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// .
	evt, err := lg.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3, map[string]interface{}{
		"id": "bel_real", "statement": "real belief", "ring": 3, "confidence": 0.5,
	}, kp)
	if err != nil {
		t.Fatal(err)
	}
	st.Materialize(evt)

	// .
	result, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{
		"content":  "an observation",
		"supports": "bel_ghost",
	})
	if err != nil {
		t.Fatalf("note itself must succeed: %v", err)
	}
	if !strings.Contains(result, "Edge refusals") || !strings.Contains(result, "bel_ghost") {
		t.Fatalf("ghost-edge refusal must be reported in the result, got: %q", result)
	}

	// .
	_, err = engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":   "edge.create",
		"edge_type": "SUPPORTS",
		"from_id":   "exp_ghost",
		"to_id":     "bel_real",
	})
	if err == nil || !strings.Contains(err.Error(), "no such entity") {
		t.Fatalf("edge.create with ghost endpoint must be refused, got: %v", err)
	}

	// .
	result, err = engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":       "belief.upsert",
		"id":            "bel_new",
		"statement":     "new belief from evidence",
		"evidence_refs": "exp_ghost1,exp_ghost2",
		"evidence":      "exp_ghost1,exp_ghost2",
	})
	if err != nil {
		t.Fatalf("belief.upsert itself must succeed: %v", err)
	}
	if !strings.Contains(result, "Edge refusals") {
		t.Fatalf("ghost evidence refusals must be reported, got: %q", result)
	}
}

// .
type discovererAdapter struct{ reg *tools.Registry }

func (d discovererAdapter) Discover(depth int) []ToolInfo {
	infos := d.reg.Discover(depth)
	out := make([]ToolInfo, 0, len(infos))
	for _, i := range infos {
		out = append(out, ToolInfo{Name: i.Name, Description: i.Description})
	}
	return out
}

// .
// .
// .
// .
// .
func TestDeliverCommitmentPathOnly(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "start", "description": "the weekly summary",
	}); err != nil {
		t.Fatal(err)
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "deliver", "result": "served: summary attached",
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

	// .
	// .
	// .
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
		"action": "deliver", "result": "served: summary attached", "commitment_id": "cm1",
	})
	if err != nil {
		t.Fatalf("deliver on commitment: %v", err)
	}
	if !strings.Contains(res, "commit commitment.state_change") {
		t.Fatalf("deliver must TEACH the conscious completion path, got: %s", res)
	}
	// .
	for _, evt := range mustReadLedger(t, lg) {
		if evt.Type == ledger.EventCommitmentStateChange {
			t.Fatal("deliver on a commitment minted commitment.state_change — completion is the resident's act, not the substrate's")
		}
	}
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant": "commitment.state_change", "id": "cm1", "state": "completed", "result": "summary attached",
	}); err != nil {
		t.Fatalf("conscious completion: %v", err)
	}
	cms, _ := st.ListCommitments(false)
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
func TestRecallCursorPagesDeepMemory(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// .
	for i := 0; i < 25; i++ {
		evt, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": fmt.Sprintf("deep%d", i), "content": fmt.Sprintf("deep memory %d", i), "category": "observation"}, kp)
		st.Materialize(evt)
	}
	// .
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

	// .
	shown, _ := st.ListExperiences(20)
	lowest := shown[len(shown)-1].CreatedSeq
	// .
	// .
	// .
	r2, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{
		"source":    "experiences",
		"after_seq": float64(lowest),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(r2, "] deep memory"); got != 6 {
		t.Fatalf("page 2 must reach the remaining 6 experiences (operator one was in page 1), got %d", got)
	}
}

// .
// .
// .
func TestToolsVerbListsOrgansFirst(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	res, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	// .
	if oi, ti := strings.Index(res, "Your organs"), strings.Index(res, "Tools in your sandbox"); oi < 0 || (ti >= 0 && oi > ti) {
		t.Fatalf("organs must lead the tools listing")
	}
	for _, organ := range []string{"note", "recall", "send", "work", "commit", "tools"} {
		if !strings.Contains(res, organ+" —") {
			t.Fatalf("organ %q must be listed", organ)
		}
	}
	// .
	// .
	for _, absorbed := range []string{"timer", "skill", "project", "curiosity", "measure"} {
		if strings.Contains(res, "  "+absorbed+" —") {
			t.Fatalf("%q is absorbed and must not be listed as an organ", absorbed)
		}
	}
	// .
	if !strings.Contains(res, "signed ledger") {
		t.Fatal("commit's description must teach what it is")
	}
}

// .
// .
// .
// .
// .
// .
func TestBeliefLadderReachesConfirmed(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	// .
	if err := engine.RecordConversationTurn("operator",
		"The witness anchor held through the whole outage."); err != nil {
		t.Fatal(err)
	}
	opTurn, err := st.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("operator turn: %v %v", opTurn, err)
	}

	// .
	engine.NoteExternalFetch("https://status.example.org/incident-7")

	// .
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
		"content":     "the operator said the anchor held through the outage",
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

	if got := mustStanding(t, st, "b_anchor_holds"); got != "confirmed" {
		t.Fatalf("three sources across three classes must derive confirmed, got %q", got)
	}

	// .
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

// .
// .
// .
func TestWorkSpawnGates(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	engine.SetAgencyLimits(2, 1, 20, 600)

	// .
	// .
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

	// .
	// .
	// .
	out, err = engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "another"})
	if err != nil || !strings.Contains(out, "Queued sub-agent") || !strings.Contains(out, "max_parallel_subagents") || !strings.Contains(out, "Do not spawn it again") {
		t.Fatalf("second spawn must queue by name: out=%q err=%v", out, err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "a third"}); err == nil ||
		!strings.Contains(err.Error(), "max_parallel_subagents") {
		t.Fatalf("beyond the queue bound the spawn must refuse with the ceiling's name: %v", err)
	}
	// .
	engine.SetSpawnQueue(false)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "refused as before"}); err == nil ||
		!strings.Contains(err.Error(), "already live") {
		t.Fatalf("queue off must refuse at the limit as before: %v", err)
	}
	engine.SetSpawnQueue(true)

	// .
	ctx := context.WithValue(context.Background(), SubagentDepth{}, 2)
	if _, err := engine.ExecuteAction(ctx, "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "too deep"}); err == nil ||
		!strings.Contains(err.Error(), "max_subagent_depth") {
		t.Fatalf("depth ceiling must refuse with its name: %v", err)
	}

	// .
	engine.SetAgencyLimits(0, 3, 20, 600)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "spawn", "goal": "x"}); err == nil ||
		!strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled spawning must refuse honestly: %v", err)
	}

	// .
	// .
	subs, err := st.CountLiveWork("subagent.run")
	if err != nil || subs != 2 {
		t.Fatalf("two live sub-agent items expected (one running, one queued): %d %v", subs, err)
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
		map[string]interface{}{"action": "deliver", "result": "served: older result"}); err != nil {
		t.Fatal(err)
	}

	var olderStatus, olderState, olderResult string
	if err := st.DB().QueryRow(
		`SELECT status, state, result FROM work_sessions WHERE id = ?`, "ws_older",
	).Scan(&olderStatus, &olderState, &olderResult); err != nil {
		t.Fatal(err)
	}
	if olderStatus != "delivered" || olderState != "older state" || olderResult != "served: older result" {
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

// .
// .
// .
// .
func TestDuplicatePushbackAndMintEnvelope(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.SetAgencyLimits(3, 3, 2, 600)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the anchor held"}); err != nil {
		t.Fatal(err)
	}
	// .
	_, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the anchor held"})
	if err == nil || !strings.Contains(err.Error(), "noticed exactly this before") {
		t.Fatalf("duplicate must bounce with the mirror: %v", err)
	}
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the anchor held", "duplicate_ok": true}); err != nil {
		t.Fatalf("override must mint: %v", err)
	}

	// .
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

	// .
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
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note",
		map[string]interface{}{"content": "the narrator still notices freely"}); err != nil {
		t.Fatalf("main-thread capture must stay frictionless: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestTheCursorRecallPrintsIsTheCursorRecallAccepts(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	for i := 0; i < 25; i++ {
		evt, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": fmt.Sprintf("deep%d", i), "content": fmt.Sprintf("deep memory %d", i), "category": "observation"}, kp)
		if err := st.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	// .
	// .
	// .
	if err := st.AddConversationTurn("operator", "deep memory conversation turn"); err != nil {
		t.Fatal(err)
	}

	page1, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{
		"source": "experiences",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []string{"Conversation", "Ledger Events", "Self-Model Syntheses", "Beliefs"} {
		if strings.Contains(page1, foreign) {
			t.Fatalf("source=experiences must read alone; %q answered too:\n%s", foreign, page1)
		}
	}

	first := renderedSeqs(t, page1)
	if len(first) == 0 {
		t.Fatalf("recall told the caller to pass back the lowest seq SHOWN, and showed none:\n%s", page1)
	}
	lowest := first[0]
	for _, s := range first {
		if s < lowest {
			lowest = s
		}
	}

	page2, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{
		"source":    "experiences",
		"after_seq": float64(lowest),
	})
	if err != nil {
		t.Fatal(err)
	}

	// .
	seen := map[int]bool{}
	for _, s := range first {
		seen[s] = true
	}
	for _, s := range renderedSeqs(t, page2) {
		if seen[s] {
			t.Fatalf("seq %d appeared on both pages — the printed cursor does not advance", s)
		}
		seen[s] = true
	}
	if len(seen) != 25 {
		t.Fatalf("the two pages must together cover all 25 experiences; covered %d", len(seen))
	}
}

// .
// .
// .
// .
func TestConversationIsASourceThatCanBeNamedAndReadAlone(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if err := st.AddConversationTurn("operator", "the archive sweep is confirmed"); err != nil {
		t.Fatal(err)
	}

	out, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{
		"source": "conversation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Conversation") || !strings.Contains(out, "archive sweep") {
		t.Fatalf("source=conversation must read the dialogue:\n%s", out)
	}
	for _, foreign := range []string{"Beliefs", "Experiences", "Ledger Events"} {
		if strings.Contains(out, foreign) {
			t.Fatalf("source=conversation must read alone; %q answered too:\n%s", foreign, out)
		}
	}
	if len(renderedSeqs(t, out)) == 0 {
		t.Fatalf("conversation must show the seq its own cursor takes:\n%s", out)
	}
}

// .
func renderedSeqs(t *testing.T, out string) []int {
	t.Helper()
	var seqs []int
	for _, m := range regexp.MustCompile(`\[seq (\d+)`).FindAllStringSubmatch(out, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("unreadable seq %q", m[1])
		}
		seqs = append(seqs, n)
	}
	return seqs
}

// .
// .
// .
// .
func TestEveryNamedSourceReadsAlone(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// .
	// .
	// .
	for i := 0; i < 3; i++ {
		evt, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": fmt.Sprintf("collide%d", i), "content": fmt.Sprintf("collide token %d", i), "category": "observation"}, kp)
		if err := st.Materialize(evt); err != nil {
			t.Fatal(err)
		}
		if err := st.AddConversationTurn("operator", fmt.Sprintf("collide token %d", i)); err != nil {
			t.Fatal(err)
		}
	}

	headings := map[string]string{
		"experiences":  "Experiences",
		"conversation": "Conversation",
		"ledger":       "Ledger Events",
		"syntheses":    "Self-Model Syntheses",
	}
	for source, own := range headings {
		out, err := engine.ExecuteAction(context.Background(), "verb", "recall",
			map[string]interface{}{"source": source})
		if err != nil {
			t.Fatalf("source=%s: %v", source, err)
		}
		for other, heading := range headings {
			if other == source {
				continue
			}
			if strings.Contains(out, heading+":") {
				t.Fatalf("source=%s also answered with %q:\n%s", source, heading, out)
			}
		}
		// .
		for _, standing := range []string{"Beliefs:", "Intentions:", "Ring State:"} {
			if strings.Contains(out, standing) {
				t.Fatalf("source=%s also answered with %q:\n%s", source, standing, out)
			}
		}
		_ = own
	}
}

// .
// .
// .
func TestPrivateRowsNeitherSurfaceNorHideThePublicOnes(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)

	// .
	// .
	for i := 0; i < 25; i++ {
		evt, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": fmt.Sprintf("priv%d", i), "content": fmt.Sprintf("private matter %d", i), "category": "observation", "private": true}, kp)
		if err := st.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		evt, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": fmt.Sprintf("pub%d", i), "content": fmt.Sprintf("public record %d", i), "category": "observation"}, kp)
		if err := st.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}

	out, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"source": "experiences"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "] public record"); got != 5 {
		t.Fatalf("all 5 public rows must be reachable past 25 private ones, got %d:\n%s", got, out)
	}
	if strings.Contains(out, "private matter") {
		t.Fatal("recall is a surfacing route — private experiences must not appear (Charter #9)")
	}
}

// .
// .
func TestRecallRefusesAnUnknownSourceAndABareCursor(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"source": "beliefs"}); err == nil {
		t.Fatal("an unpaged standing group is not a nameable source — must refuse rather than silently read all")
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"after_seq": float64(4)}); err == nil {
		t.Fatal("a bare cursor means a different position in each source — must refuse")
	}
}

// .
// .
func mustStanding(t *testing.T, src interface {
	StandingFor(id string) (string, error)
}, id string) string {
	t.Helper()
	standing, err := src.StandingFor(id)
	if err != nil {
		t.Fatalf("standing of %s: %v", id, err)
	}
	return standing
}

// .
// .
// .
// .
// .
func TestPromoteRefusesWhenStandingCannotBeProven(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{
			"variant": "belief.upsert", "id": "b_unprovable",
			"statement": "a claim", "confidence": 0.5, "evidence_refs": "none",
		}); err != nil {
		t.Fatalf("belief.upsert setup: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit",
		map[string]interface{}{"variant": "belief.promote", "id": "b_unprovable", "ring": 2})
	if err == nil {
		t.Fatal("promote proceeded on a standing that cannot be proven")
	}
	if !strings.Contains(err.Error(), "cannot be proven") {
		t.Fatalf("refusal does not name the real cause: %v", err)
	}
}

// .
// .
// .
// .
func TestWorkDeliverRequiresOutcomePrefixBeforeMutation(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "start", "description": "deliver contract"}); err != nil {
		t.Fatalf("work start failed: %v", err)
	}
	ws, err := st.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("active work session: ws=%v err=%v", ws, err)
	}

	_, err = engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "finished without a verdict"})
	if err == nil || !strings.Contains(err.Error(), "served:, partial: or unserved:") {
		t.Fatalf("unprefixed delivery must be refused by the shared outcome contract, teaching the form: %v", err)
	}

	var status, result string
	var harvestedIsNull int
	if err := st.DB().QueryRow(
		`SELECT status, COALESCE(result,''), harvested_ms IS NULL FROM work_sessions WHERE id = ?`, ws.ID,
	).Scan(&status, &result, &harvestedIsNull); err != nil {
		t.Fatal(err)
	}
	if status != "active" || result != "" || harvestedIsNull != 1 {
		t.Fatalf("a rejected delivery mutated the session: status=%q result=%q harvested_is_null=%d", status, result, harvestedIsNull)
	}

	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": ""}); err == nil || !strings.Contains(err.Error(), "served:, partial: or unserved:") {
		t.Fatalf("an empty delivery must be refused the same way: %v", err)
	}

	const delivered = "served: focused contract test passed"
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": delivered}); err != nil {
		t.Fatalf("the prefixed retry must remain deliverable: %v", err)
	}
	if err := st.DB().QueryRow(
		`SELECT status, COALESCE(result,'') FROM work_sessions WHERE id = ?`, ws.ID,
	).Scan(&status, &result); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || result != delivered {
		t.Fatalf("prefixed delivery = status %q, result %q", status, result)
	}
}
