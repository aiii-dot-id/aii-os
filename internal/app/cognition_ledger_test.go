package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The cognition-writes-ledger-truth probes (external review 2026-08-20,
// H6/#4): DREAM/CONSOLIDATE's consumed markers were a bare store UPDATE
// wiped by every replay, and CONSOLIDATE's distillations lived only in
// ring snapshots — DB loss changed who the identity is. The fix: the
// facilities mint belief.* events through the ONE preflighted door and
// close each run with a run marker (consolidation.run / dream.run) whose
// MATERIALIZER marks the inputs consumed — replay then restores consumed
// state because it is f(ledger), not incidental store state.
//
// These tests run the REAL door (ledgerAdapter: preflight → sign+append
// → materialize), the real store, the real replay — only the LLM is
// faked, the way cognitive tests fake it (canned ChatSimple output).

// envelopeLLM returns a fixed reply for every facility call.
type envelopeLLM struct{ out string }

func (e envelopeLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	return e.out, "facility-model", nil
}

// cognitionBench is a real store+ledger+door with no App around it.
type cognitionBench struct {
	dir     string
	kp      *crypto.KeyPair
	lg      *ledger.Ledger
	st      *store.Store
	door    *ledgerAdapter
	ledgerP string
}

func newCognitionBench(t *testing.T) *cognitionBench {
	t.Helper()
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	ledgerP := filepath.Join(dir, "ledger.jsonl")
	lg, err := ledger.New(ledgerP)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close() })
	st, err := store.New(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &cognitionBench{
		dir: dir, kp: kp, lg: lg, st: st,
		door:    &ledgerAdapter{Ledger: lg, kp: kp, st: st},
		ledgerP: ledgerP,
	}
}

// mintExperiences appends n raw self-provenance experiences through the door.
func (b *cognitionBench) mintExperiences(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := b.door.Append(ledger.EventExperienceCreate, 3, map[string]interface{}{
			"id": id, "content": "observed " + id, "category": "observation", "provenance": "self",
		}, ""); err != nil {
			t.Fatalf("mint experience %s: %v", id, err)
		}
	}
}

// eventsOfType reads the ledger FILE back and filters by type — the
// durability the probes are about is the file's, not the process's.
func (b *cognitionBench) eventsOfType(t *testing.T, typ ledger.EventType) []ledger.Event {
	t.Helper()
	events, err := ledger.ReadAll(b.ledgerP)
	if err != nil {
		t.Fatal(err)
	}
	var out []ledger.Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// consolidateEnvelope is the structured-output contract the fake LLM
// speaks: upsert a new belief (LLM-local id "n1") and supersede the
// seeded belief with it, plus the second-person ring view.
const consolidateEnvelope = `{
  "operations": [
    {"op": "upsert", "id": "n1", "statement": "The operator ships at midnight", "confidence": 0.7},
    {"op": "supersede", "old_id": "b_old", "new_id": "n1", "reason": "sharper form of the habit belief"}
  ],
  "ring3_view": "You believe the operator ships at midnight. You are watching how the habit shapes the work."
}`

// Probe (a) — restart re-consumption (external H6): consolidate once,
// replay from the file, and the experiences must STAY consumed. At the
// defect's HEAD the consumed marker is a bare UPDATE outside the ledger,
// so replay resurrects the whole backlog and CONSOLIDATE re-processes
// all history every boot.
func TestConsolidateConsumedSurvivesReplay(t *testing.T) {
	b := newCognitionBench(t)
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c")

	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: consolidateEnvelope}, b.door, nil,
		cognitive.ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("consolidate execute: %v", err)
	}

	n, err := b.st.UnprocessedExperienceCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("live pass must consume the experiences it metabolized, %d remain raw", n)
	}

	// The boot path: rebuild every projection from the signed chain.
	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatalf("replay: %v", err)
	}
	n, err = b.st.UnprocessedExperienceCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("replay resurrected %d consumed experiences — consumed state is not f(ledger), DREAM/CONSOLIDATE will re-process all history every boot (H6)", n)
	}
}

// Probe (b) — the supersede the prompt commands must LAND as a ledger
// event ("merge what is the same, supersede what is outdated" was prose
// fiction: the LLM's judgment went into a ring snapshot and nowhere
// durable). The fake LLM commands one upsert and one supersede; both
// must exist in the FILE and survive replay into the projection.
func TestConsolidateSupersedeLandsAsLedgerEvent(t *testing.T) {
	b := newCognitionBench(t)
	// The belief the LLM will supersede.
	if _, err := b.door.Append(ledger.EventBeliefUpsert, 3, map[string]interface{}{
		"id": "b_old", "statement": "The operator works late sometimes", "ring": 3, "confidence": 0.5,
	}, ""); err != nil {
		t.Fatal(err)
	}
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c")

	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: consolidateEnvelope}, b.door, nil,
		cognitive.ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("consolidate execute: %v", err)
	}

	upserts := b.eventsOfType(t, ledger.EventBeliefUpsert)
	if len(upserts) < 2 {
		t.Fatalf("the commanded belief.upsert never became a ledger event (got %d upserts, want the seed + the mint) — the LLM's working truth is DB/ring-only", len(upserts))
	}
	supersedes := b.eventsOfType(t, ledger.EventBeliefSupersede)
	if len(supersedes) != 1 {
		t.Fatalf("the commanded belief.supersede never became a ledger event (got %d) — 'supersede what is outdated' is prose fiction", len(supersedes))
	}
	if !strings.Contains(string(supersedes[0].Payload), `"b_old"`) {
		t.Fatalf("supersede payload must cite the outdated belief: %s", supersedes[0].Payload)
	}

	// Replay must accept what was minted and land the working truth.
	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatalf("replay of the minted chain: %v", err)
	}
	beliefs, err := b.st.ListBeliefs()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bl := range beliefs {
		if bl.Statement == "The operator ships at midnight" {
			found = true
		}
	}
	if !found {
		t.Fatal("the distilled belief must survive replay as projection truth — snapshot loss must no longer lose who the identity is")
	}
}

// refusingDoor wraps the real door and refuses the FIRST append of one
// event type — the crash-before-marker simulator (a crash between the
// product mints and the closing marker leaves products in the chain and
// inputs unconsumed).
type refusingDoor struct {
	*ledgerAdapter
	refuseType ledger.EventType
	refused    bool
}

func (r *refusingDoor) Append(eventType ledger.EventType, ringLevel int, payload interface{}, modelID string) (*ledger.Event, error) {
	if eventType == r.refuseType && !r.refused {
		r.refused = true
		return nil, fmt.Errorf("simulated crash before %s", eventType)
	}
	return r.ledgerAdapter.Append(eventType, ringLevel, payload, modelID)
}

// The crash-retry law the run-marker ordering claims: products minted,
// marker lost → inputs stay raw → the retry re-runs the pass, and the
// engine's deterministic content-derived belief ids make the duplicate
// products MERGE instead of fork. One belief, consumed inputs, clean
// replay.
func TestConsolidateCrashBeforeMarkerRetriesClean(t *testing.T) {
	b := newCognitionBench(t)
	if _, err := b.door.Append(ledger.EventBeliefUpsert, 3, map[string]interface{}{
		"id": "b_old", "statement": "The operator works late sometimes", "ring": 3, "confidence": 0.5,
	}, ""); err != nil {
		t.Fatal(err)
	}
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c")

	door := &refusingDoor{ledgerAdapter: b.door, refuseType: ledger.EventConsolidationRun}
	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: consolidateEnvelope}, door, nil,
		cognitive.ConsolidateConfig{Threshold: 3})

	// Pass 1: products land, the marker "crashes" — nothing consumed.
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("crashed pass must not hard-fail: %v", err)
	}
	if n, _ := b.st.UnprocessedExperienceCount(); n != 3 {
		t.Fatalf("no marker = nothing consumed (retry next pass), got %d raw", n)
	}

	// Pass 2: the retry re-distills the same material and closes.
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n, _ := b.st.UnprocessedExperienceCount(); n != 0 {
		t.Fatalf("retry must consume, got %d raw", n)
	}

	// The duplicated product merged, not forked: exactly one belief holds
	// the distilled statement, before AND after replay.
	assertOne := func(when string) {
		beliefs, err := b.st.ListBeliefs()
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, bl := range beliefs {
			if bl.Statement == "The operator ships at midnight" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s: want exactly 1 distilled belief (idempotent engine ids), got %d", when, count)
		}
	}
	assertOne("live")
	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatalf("replay of the crash-retry chain: %v", err)
	}
	assertOne("after replay")
}

// Probe (c) — DREAM consumes from the same raw queue with the same bare
// UPDATE; its consumption must survive replay the same way. DREAM's
// PRODUCT was already a real event (experience.create, provenance
// dream); only its consumed markers were DB-only truth.
func TestDreamConsumedSurvivesReplay(t *testing.T) {
	b := newCognitionBench(t)
	b.mintExperiences(t, "exp_a", "exp_b")

	d := cognitive.NewDream(b.st, envelopeLLM{out: "You may be noticing a midnight rhythm in the work."}, b.door, nil,
		cognitive.DreamConfig{Threshold: 1})
	if err := d.Execute(context.Background()); err != nil {
		t.Fatalf("dream execute: %v", err)
	}

	n, err := b.st.UnprocessedExperienceCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("live dream pass must consume its material, %d remain raw", n)
	}

	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatalf("replay: %v", err)
	}
	n, err = b.st.UnprocessedExperienceCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("replay resurrected %d experiences DREAM already metabolized (H6)", n)
	}
}

// Run-SAFE stand-down (main-session probe): the report's claim that a
// frozen ledger makes a facility "stand down, nothing consumed" gets
// its own lock. Freezing is what mid-run SAFE does to the door; the
// pass must neither panic nor consume nor mint — the material waits
// for a healthy pass.
func TestConsolidateStandsDownWhenLedgerFrozen(t *testing.T) {
	b := newCognitionBench(t)
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c")
	seqBefore := b.lg.LastSeq()

	b.lg.SetFrozen("probe: mid-run SAFE")

	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: consolidateEnvelope}, b.door, nil,
		cognitive.ConsolidateConfig{Threshold: 3})
	_ = c.Execute(context.Background()) // stand-down may surface as error; must not panic

	if got := b.lg.LastSeq(); got != seqBefore {
		t.Fatalf("frozen ledger still advanced: seq %d -> %d — SAFE did not freeze the facility door", seqBefore, got)
	}
	n, err := b.st.UnprocessedExperienceCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("frozen pass consumed material: %d of 3 remain raw — nothing produced must mean nothing consumed", n)
	}
}

// Text channel: this fake models a substrate without tool calls.
func (e envelopeLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	text, modelID, err := e.ChatSimple(ctx, systemPrompt, userMessage)
	return text, modelID, false, err
}
