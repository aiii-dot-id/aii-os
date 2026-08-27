package identity

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func selfModelSources(t *testing.T, st *store.Store, lg *ledger.Ledger, kp *crypto.KeyPair) []map[string]interface{} {
	t.Helper()
	mint := func(typ ledger.EventType, ring int, payload map[string]interface{}) {
		evt, err := lg.Append(typ, kp.Fingerprint(), ring, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	mint(ledger.EventBeliefUpsert, 3, map[string]interface{}{"id": "b_self", "statement": "Evidence matters", "ring": 3, "confidence": 0.8})
	mint(ledger.EventExperienceCreate, 3, map[string]interface{}{"id": "x_self", "content": "A failure exposed the truth", "category": "observation"})
	mint(ledger.EventIntentionCreate, 3, map[string]interface{}{"id": "i_self", "statement": "Keep improving"})
	mint(ledger.EventRelationshipUpsert, 1, map[string]interface{}{
		"id": "rel_self", "counterpart_name": "Peer", "counterpart_role": "peer",
		"relationship_type": "peer", "charter_text": "Work honestly together",
	})
	return []map[string]interface{}{
		{"class": "beliefs", "id": "b_self"},
		{"class": "experiences", "id": "x_self"},
		{"class": "intentions", "id": "i_self"},
		{"class": "relationships", "id": "rel_self"},
	}
}

func selfModelArgs(id string, refs []map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"variant": "self_model.synthesize", "id": id,
		"synthesis_text":     "I am learning to trust evidence.",
		"continuity_thread":  "I remain careful and curious.",
		"source_entity_refs": refs,
	}
}

func TestSelfModelNoChangeAndReplay(t *testing.T) {
	engine, st, lg, kp, dir := setupEngine(t)
	refs := selfModelSources(t, st, lg, kp)
	lg.SetModelID("configured-model") // production stamps model_id before materialization
	beforeRejected := lg.LastSeq()
	tooNarrow := selfModelArgs("syn_narrow", refs[:3])
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", tooNarrow); err == nil {
		t.Fatal("three source classes must be refused")
	}
	if lg.LastSeq() != beforeRejected {
		t.Fatal("refused synthesis reached the ledger")
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", selfModelArgs("syn_one", refs)); err != nil {
		t.Fatal(err)
	}
	before := lg.LastSeq()
	second := selfModelArgs("syn_two", refs)
	second["previous_synthesis_id"] = "syn_one"
	result, err := engine.ExecuteAction(context.Background(), "verb", "commit", second)
	if err != nil || !strings.HasPrefix(result, "No change:") {
		t.Fatalf("exact duplicate must be a successful no-op: %q %v", result, err)
	}
	if lg.LastSeq() != before {
		t.Fatalf("no-change advanced ledger %d -> %d", before, lg.LastSeq())
	}

	rebuilt, err := store.New(filepath.Join(dir, "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	if err := rebuilt.ReplayFromFile(filepath.Join(dir, "ledger.jsonl")); err != nil {
		t.Fatal(err)
	}
	current, err := rebuilt.CurrentSelfModel()
	if err != nil || current == nil || current.ID != "syn_one" {
		t.Fatalf("replay current portrait = %+v, %v", current, err)
	}
}

func TestSelfModelConcurrentPredecessorAdmitsOne(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)
	refs := selfModelSources(t, st, lg, kp)
	before := lg.LastSeq()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"syn_a", "syn_b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, err := engine.ExecuteAction(context.Background(), "verb", "commit", selfModelArgs(id, refs))
			errs <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(errs)
	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 || lg.LastSeq() != before+1 {
		t.Fatalf("concurrent syntheses: successes=%d ledger delta=%d", succeeded, lg.LastSeq()-before)
	}
}

func TestSelfModelRejectsModelSuppliedModelID(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)
	refs := selfModelSources(t, st, lg, kp)
	before := lg.LastSeq()
	args := selfModelArgs("syn_forged_model", refs)
	args["model_id"] = "model-chosen-provenance"
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", args); !errors.Is(err, ledger.ErrModelIDOwned) {
		t.Fatalf("model-supplied provenance must be refused, got %v", err)
	}
	if lg.LastSeq() != before {
		t.Fatalf("refused model_id reached the ledger: seq %d -> %d", before, lg.LastSeq())
	}
}
