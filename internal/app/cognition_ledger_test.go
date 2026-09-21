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

// .
type envelopeLLM struct{ out string }

func (e envelopeLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	return e.out, "facility-model", nil
}

// .
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

// .
func (b *cognitionBench) mintExperiences(t *testing.T, ids ...string) {
	t.Helper()
	b.mintExperiencesWith(t, "self", ids...)
}

// .
func (b *cognitionBench) mintExperiencesWith(t *testing.T, provenance string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		payload := map[string]interface{}{
			"id": id, "content": "observed " + id, "category": "observation", "provenance": provenance,
		}
		if provenance == "external" {
			payload["source_url"] = "https://review.example/" + id
		}
		if _, err := b.door.Append(ledger.EventExperienceCreate, 3, payload, ""); err != nil {
			t.Fatalf("mint experience %s: %v", id, err)
		}
	}
}

// .
// .
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

// .
// .
// .
const consolidateEnvelope = `{
  "operations": [
    {"op": "upsert", "id": "n1", "statement": "The operator ships at midnight", "confidence": 0.7, "evidence": ["exp_a", "exp_b", "exp_c"]},
    {"op": "supersede", "old_id": "b_old", "new_id": "n1", "reason": "sharper form of the habit belief"}
  ],
  "ring3_view": "You believe the operator ships at midnight. You are watching how the habit shapes the work."
}`

// .
// .
// .
// .
// .
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

	// .
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

// .
// .
// .
// .
// .
func TestConsolidateSupersedeLandsAsLedgerEvent(t *testing.T) {
	b := newCognitionBench(t)
	// .
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

	// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
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

	// .
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("crashed pass must not hard-fail: %v", err)
	}
	if n, _ := b.st.UnprocessedExperienceCount(); n != 3 {
		t.Fatalf("no marker = nothing consumed (retry next pass), got %d raw", n)
	}

	// .
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n, _ := b.st.UnprocessedExperienceCount(); n != 0 {
		t.Fatalf("retry must consume, got %d raw", n)
	}

	// .
	// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
func TestAnEmptyDreamPassConsumesThroughTheRealDoorAndSurvivesReplay(t *testing.T) {
	b := newCognitionBench(t)
	b.mintExperiences(t, "exp_a", "exp_b")
	before := b.lg.LastSeq()

	d := cognitive.NewDream(b.st, envelopeLLM{out: "NOTHING SURFACED"}, b.door, nil,
		cognitive.DreamConfig{Threshold: 1})
	if err := d.Execute(context.Background()); err != nil {
		t.Fatalf("dream execute: %v", err)
	}
	if got := b.lg.LastSeq(); got != before+1 {
		t.Fatalf("an empty pass appended %d record(s), want the marker alone", got-before)
	}
	check := func(when string) {
		t.Helper()
		n, err := b.st.UnprocessedExperienceCount()
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s: %d experiences are raw again — the empty pass will be repeated forever", when, n)
		}
		var dreams int
		if err := b.st.DB().QueryRow(`SELECT COUNT(*) FROM experiences WHERE provenance = 'dream'`).Scan(&dreams); err != nil {
			t.Fatal(err)
		}
		if dreams != 0 {
			t.Fatalf("%s: an empty pass minted %d dream note(s)", when, dreams)
		}
	}
	check("live")
	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatalf("replay of a marker with no outputs: %v", err)
	}
	check("after replay")
}

// .
func TestAnOversizedDreamReplyAppendsNothing(t *testing.T) {
	b := newCognitionBench(t)
	b.mintExperiences(t, "exp_a")
	before := b.lg.LastSeq()
	d := cognitive.NewDream(b.st, envelopeLLM{out: strings.Repeat("Let me think about this. ", 200)}, b.door, nil,
		cognitive.DreamConfig{Threshold: 1})
	if err := d.Execute(context.Background()); err != nil {
		t.Fatalf("dream execute: %v", err)
	}
	if got := b.lg.LastSeq(); got != before {
		t.Fatalf("a refused note still appended %d record(s)", got-before)
	}
	if n, _ := b.st.UnprocessedExperienceCount(); n != 1 {
		t.Fatalf("a refused pass consumed its material: %d raw, want 1", n)
	}
}

// .
type capturingLLM struct {
	out  string
	user *string
}

func (c capturingLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	*c.user = userMessage
	return c.out, "test-model", nil
}

func (c capturingLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	*c.user = userMessage
	return c.out, "test-model", false, nil
}

// .
// .
// .
// .
// .
// .
// .
func TestAContestedBeliefThroughTheRealStore(t *testing.T) {
	const sealed = "THE SEALED WORDS nobody but the identity may read"
	b := newCognitionBench(t)
	mint := func(et ledger.EventType, payload map[string]interface{}) {
		t.Helper()
		if _, err := b.door.Append(et, 3, payload, ""); err != nil {
			t.Fatalf("mint %s: %v", et, err)
		}
	}
	b.mintExperiences(t, "exp_seed")
	mint(ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_old", "statement": "The operator ships in the morning", "ring": 3, "confidence": 0.5})
	mint(ledger.EventExperienceCreate, map[string]interface{}{
		"id": "exp_sealed", "content": sealed, "category": "reflection", "provenance": "self", "private": true, "raw": false,
	})
	mint(ledger.EventEdgeCreate, map[string]interface{}{"id": "edge_t1", "from_id": "exp_sealed", "to_id": "b_old", "edge_type": "CONTRADICTS"})
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c")

	var shown string
	c := cognitive.NewConsolidate(b.st, capturingLLM{out: consolidateEnvelope, user: &shown}, b.door, nil,
		cognitive.ConsolidateConfig{Threshold: 3})
	c.SetTensions(b.st)
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("consolidate execute: %v", err)
	}

	if strings.Contains(shown, sealed) {
		t.Fatal("A PRIVATE NOTE'S WORDS REACHED CONSOLIDATE THROUGH THE REAL STORE")
	}
	for _, want := range []string{"[b_old, suspect]", "[exp_sealed] a private note (sealed: its content is not shown) stands against [b_old]"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the pass was not shown %q:\n%s", want, shown)
		}
	}
	if got := b.eventsOfType(t, ledger.EventBeliefSupersede); len(got) != 0 {
		t.Fatalf("A CONTESTED BELIEF WAS RETIRED BY CONSOLIDATION: %d supersede(s) in the record", len(got))
	}
	// .
	// .
	var minted bool
	for _, e := range b.eventsOfType(t, ledger.EventBeliefUpsert) {
		if strings.Contains(string(e.Payload), "The operator ships at midnight") {
			minted = true
		}
	}
	if !minted {
		t.Fatal("the refused supersede took the rest of the envelope with it: the upsert was not minted")
	}
	// .
	pairs, err := b.st.TensionsView()
	if err != nil || len(pairs) != 1 {
		t.Fatalf("the contradiction did not survive the pass: %d %v", len(pairs), err)
	}
	if standing, err := b.st.StandingFor("b_old"); err != nil || standing != "suspect" {
		t.Fatalf("b_old reads %q (%v), want suspect", standing, err)
	}
}

// .
// .
// .
// .
// .
func TestConsolidateStandsDownWhenLedgerFrozen(t *testing.T) {
	b := newCognitionBench(t)
	b.mintExperiences(t, "exp_a", "exp_b", "exp_c")
	seqBefore := b.lg.LastSeq()

	b.lg.SetFrozen("probe: mid-run SAFE")

	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: consolidateEnvelope}, b.door, nil,
		cognitive.ConsolidateConfig{Threshold: 3})
	_ = c.Execute(context.Background())

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

// .
func (e envelopeLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	text, modelID, err := e.ChatSimple(ctx, systemPrompt, userMessage)
	return text, modelID, false, err
}

// .
// .
// .
// .
// .
func TestConsolidateEvidenceMakesStandingDerivable(t *testing.T) {
	b := newCognitionBench(t)
	b.mintExperiencesWith(t, "external", "exp_a", "exp_b")
	b.mintExperiences(t, "exp_c")
	c := cognitive.NewConsolidate(b.st, envelopeLLM{out: `{
  "operations": [{"op": "upsert", "id": "n1", "statement": "The figure held under review", "confidence": 0.7, "evidence": ["exp_a", "exp_b", "exp_c"]}],
  "ring3_view": "You believe the figure held under review."
}`}, b.door, nil, cognitive.ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	standingOf := func() string {
		beliefs, err := b.st.ListBeliefs()
		if err != nil {
			t.Fatal(err)
		}
		for _, bl := range beliefs {
			if bl.Statement == "The figure held under review" {
				standing, err := b.st.StandingFor(bl.ID)
				if err != nil {
					t.Fatal(err)
				}
				return standing
			}
		}
		t.Fatal("the consolidated belief is missing")
		return ""
	}
	if got := standingOf(); got != "confirmed" {
		t.Fatalf("standing = %q, want confirmed from three cited sources across two classes", got)
	}
	if edges := b.eventsOfType(t, ledger.EventEdgeCreate); len(edges) != 3 {
		t.Fatalf("want three DERIVED_FROM edge events in the file, got %d", len(edges))
	}
	if err := b.st.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatal(err)
	}
	if got := standingOf(); got != "confirmed" {
		t.Fatalf("standing after replay = %q — the evidence did not survive a rebuild", got)
	}
}
