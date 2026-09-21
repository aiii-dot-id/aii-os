package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .

// .
// .
type scriptedLLM struct {
	replies []string
	errs    []error
	shown   []string
	system  []string
}

func (s *scriptedLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	i := len(s.shown)
	s.shown = append(s.shown, userMessage)
	s.system = append(s.system, systemPrompt)
	if i < len(s.errs) && s.errs[i] != nil {
		return "", "", s.errs[i]
	}
	if i < len(s.replies) {
		return s.replies[i], "facility-model", nil
	}
	return "", "", errors.New("the script has no reply for this call")
}

// .
// .
func (s *scriptedLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	return "", "", false, errors.New("the outcome intake makes no structured call")
}

// .
// .
type countingOwner struct {
	name string
	runs int
}

func (c *countingOwner) Name() string { return c.name }
func (c *countingOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) cognitive.AlarmResult {
	c.runs++
	return cognitive.AlarmResult{Accepted: true}
}

type openGate struct{}

func (openGate) TryBeginTurn() bool { return true }
func (openGate) EndTurn()           {}

// .
// .
type outcomeBench struct {
	*cognitionBench
	llm *scriptedLLM
	fac *cognitive.ConsolidateFacility
}

func newOutcomeBench(t *testing.T, llm *scriptedLLM, src cognitive.OutcomeSource) *outcomeBench {
	t.Helper()
	b := newCognitionBench(t)
	evt, err := b.lg.Append(ledger.EventRing0Genesis, b.kp.Fingerprint(), 0,
		map[string]string{"name": "Outcomes", "fingerprint": b.kp.Fingerprint()}, b.kp)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.st.Materialize(evt); err != nil {
		t.Fatal(err)
	}
	ob := &outcomeBench{cognitionBench: b, llm: llm}
	ob.mint(t, ledger.EventRelationshipUpsert, map[string]string{
		"id": "nova", "counterpart_name": "Nova", "counterpart_role": "peer", "relationship_type": "peer"})
	ob.fac = ob.facility(benchWindow)
	if src != nil {
		ob.fac.SetOutcomes(src)
	}
	return ob
}

// .
// .
// .
func (b *outcomeBench) facility(window int) *cognitive.ConsolidateFacility {
	cfg := *defaultConfig()
	cfg.Prompt.SurfacingMaxChars = benchObservationBound
	cfg.Agency.OutcomeWindow = window
	fac := cognitive.NewConsolidate(b.st, b.llm, b.door, nil, consolidateConfig(cfg))
	fac.SetOutcomes(b.st)
	return fac
}

func (b *outcomeBench) mint(t *testing.T, typ ledger.EventType, payload interface{}) *ledger.Event {
	t.Helper()
	evt, err := b.door.Append(typ, ledger.CanonicalRings(typ)[0], payload, "")
	if err != nil {
		t.Fatalf("mint %s: %v", typ, err)
	}
	return evt
}

// .
// .
func (b *outcomeBench) rhythm() (*cognitive.Rhythm, *countingOwner, *countingOwner) {
	dream, consolidate := &countingOwner{name: "dream"}, &countingOwner{name: "consolidate"}
	r := cognitive.NewRhythm(b.st, openGate{}, dream, consolidate, &countingOwner{name: "self_model"}, &countingOwner{name: "identity_review"})
	r.SetOutcomes(b.fac)
	return r, dream, consolidate
}

func (b *outcomeBench) tick(r *cognitive.Rhythm) {
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
}

// .
func (b *outcomeBench) twoOutcomes(t *testing.T) (done, dropped *ledger.Event) {
	t.Helper()
	b.mint(t, ledger.EventIntentionCreate, map[string]string{"id": "i1", "statement": "learn the recovery drill by heart"})
	b.mint(t, ledger.EventCommitmentPromised, map[string]string{"id": "c1", "description": "send Nova the escrow receipt", "counterpart_id": "nova"})
	done = b.mint(t, ledger.EventIntentionStateChange, map[string]string{"id": "i1", "state": "completed", "outcome": "ran it three times without the page"})
	dropped = b.mint(t, ledger.EventCommitmentStateChange, map[string]string{"id": "c1", "state": "abandoned", "repair_state": "owed", "note": "the host was down all week"})
	return done, dropped
}

type citedObservation struct {
	ID         string            `json:"id"`
	Content    string            `json:"content"`
	Provenance string            `json:"provenance"`
	Cites      []ledger.Citation `json:"cites"`
}

func (b *outcomeBench) observations(t *testing.T) []citedObservation {
	t.Helper()
	var out []citedObservation
	for _, e := range b.eventsOfType(t, ledger.EventExperienceCreate) {
		var o citedObservation
		if err := json.Unmarshal(e.Payload, &o); err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

// .
// .
// .
const benchObservationBound = 200

// .
// .
const benchWindow = 64

const anObservation = "I finish what I can rehearse alone and drop what waits on a host I do not control."

// .
// .
// .
func TestOutcomesBecomeOneCitedRawObservation(t *testing.T) {
	llm := &scriptedLLM{replies: []string{anObservation}}
	b := newOutcomeBench(t, llm, nil)
	done, dropped := b.twoOutcomes(t)

	r, dream, consolidate := b.rhythm()
	b.tick(r)

	if len(llm.shown) != 1 {
		t.Fatalf("the model was called %d times, want once", len(llm.shown))
	}
	for _, want := range []string{"learn the recovery drill by heart", "ran it three times without the page",
		"send Nova the escrow receipt", "repair: owed", "the host was down all week", "completed", "abandoned"} {
		if !strings.Contains(llm.shown[0], want) {
			t.Errorf("the intake was not shown %q:\n%s", want, llm.shown[0])
		}
	}
	obs := b.observations(t)
	if len(obs) != 1 {
		t.Fatalf("the record holds %d observations, want exactly one", len(obs))
	}
	o := obs[0]
	if o.Content != anObservation || o.Provenance != "self" {
		t.Fatalf("the observation is %q by %q, want the model's words as the identity's own", o.Content, o.Provenance)
	}
	want := []ledger.Citation{
		{Identity: b.kp.Fingerprint(), Seq: done.Seq, EntryHash: done.EntryHash()},
		{Identity: b.kp.Fingerprint(), Seq: dropped.Seq, EntryHash: dropped.EntryHash()},
	}
	if len(o.Cites) != len(want) {
		t.Fatalf("the observation cites %d records, want %d", len(o.Cites), len(want))
	}
	for i := range want {
		if o.Cites[i] != want[i] {
			t.Errorf("citation %d is %+v, want the record's own tuple %+v", i, o.Cites[i], want[i])
		}
	}
	// .
	raw, err := b.st.ListRawExperiences(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw[0].ID != o.ID {
		t.Fatalf("the raw queue holds %+v, want the observation", raw)
	}
	// .
	if n := len(b.eventsOfType(t, ledger.EventBeliefUpsert)); n != 0 {
		t.Fatalf("the intake minted %d beliefs", n)
	}
	if got, _ := b.st.OutcomeCursor(); got != dropped.Seq {
		t.Fatalf("the cursor reads %d, want %d", got, dropped.Seq)
	}
	if dream.runs != 0 || consolidate.runs != 0 {
		t.Fatalf("the raw path also ran on the intake's delta: dream=%d consolidate=%d", dream.runs, consolidate.runs)
	}

	// .
	// .
	r2, _, consolidate2 := b.rhythm()
	b.tick(r2)
	if len(llm.shown) != 1 || len(b.observations(t)) != 1 {
		t.Fatalf("after a restart the model was called %d times and the record holds %d observations", len(llm.shown), len(b.observations(t)))
	}
	if consolidate2.runs != 1 {
		t.Fatalf("the observation waits raw and consolidation ran %d times", consolidate2.runs)
	}
}

// .
// .
func TestAFailedIntakePublishesNothing(t *testing.T) {
	llm := &scriptedLLM{errs: []error{errors.New("provider down")}, replies: []string{"", anObservation}}
	b := newOutcomeBench(t, llm, nil)
	b.twoOutcomes(t)

	r, _, _ := b.rhythm()
	b.tick(r)
	b.tick(r)
	if len(llm.shown) != 1 {
		t.Fatalf("a failing intake called the model %d times in two ticks, want 1", len(llm.shown))
	}
	if got, _ := b.st.OutcomeCursor(); got != 0 || len(b.observations(t)) != 0 {
		t.Fatalf("a failed intake left cursor %d and %d observations, want nothing published", got, len(b.observations(t)))
	}
	r2, _, _ := b.rhythm()
	b.tick(r2)
	if len(b.observations(t)) != 1 {
		t.Fatalf("the outcomes did not wait: %d observations after the retry", len(b.observations(t)))
	}
}

// .
// .
func TestAnHonestNothingMovesTheCursorAndMintsNothing(t *testing.T) {
	llm := &scriptedLLM{replies: []string{"  NOTHING SURFACED\n"}}
	b := newOutcomeBench(t, llm, nil)
	_, dropped := b.twoOutcomes(t)

	r, _, _ := b.rhythm()
	b.tick(r)
	if n := len(b.observations(t)); n != 0 {
		t.Fatalf("the word for nothing was minted into the record as %d observation(s)", n)
	}
	if got, _ := b.st.OutcomeCursor(); got != dropped.Seq {
		t.Fatalf("the cursor reads %d, want %d — an honest nothing consumes what it read", got, dropped.Seq)
	}
	r2, _, _ := b.rhythm()
	b.tick(r2)
	if len(llm.shown) != 1 {
		t.Fatalf("outcomes already read were put to the model again (%d calls)", len(llm.shown))
	}
}

// .
// .
func TestAnEmptyReplyIsNotAnHonestNothing(t *testing.T) {
	llm := &scriptedLLM{replies: []string{"  \n"}}
	b := newOutcomeBench(t, llm, nil)
	b.twoOutcomes(t)
	if err := b.fac.ObserveOutcomes(context.Background()); err == nil {
		t.Fatal("an empty reply was taken for a product")
	}
	if got, _ := b.st.OutcomeCursor(); got != 0 || len(b.observations(t)) != 0 {
		t.Fatalf("an empty reply left cursor %d and %d observations, want nothing published", got, len(b.observations(t)))
	}
}

// .
// .
func TestAnObservationOverTheBoundIsRefusedWhole(t *testing.T) {
	over := strings.Repeat("é", benchObservationBound+1)
	llm := &scriptedLLM{replies: []string{over}}
	b := newOutcomeBench(t, llm, nil)
	b.twoOutcomes(t)

	err := b.fac.ObserveOutcomes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "prompt.surfacing_max_chars") {
		t.Fatalf("an observation over the bound got %v, want a refusal naming the bound", err)
	}
	if got, _ := b.st.OutcomeCursor(); got != 0 || len(b.observations(t)) != 0 {
		t.Fatalf("a refused observation left cursor %d and %d observations", got, len(b.observations(t)))
	}
	// .
	llm.replies = append(llm.replies, strings.Repeat("é", benchObservationBound))
	if err := b.fac.ObserveOutcomes(context.Background()); err != nil {
		t.Fatalf("an observation at the bound was refused: %v", err)
	}
}

// .
// .
type losingSource struct{ *store.Store }

func (losingSource) PublishOutcomeCursor(from, through uint64) error {
	return errors.New("the process died here")
}

// .
// .
// .
func TestALostCursorNeverObservesAnOutcomeTwice(t *testing.T) {
	llm := &scriptedLLM{replies: []string{anObservation, "a second observation of the same outcomes"}}
	b := newOutcomeBench(t, llm, nil)
	b.fac.SetOutcomes(losingSource{b.st})
	b.twoOutcomes(t)

	if err := b.fac.ObserveOutcomes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.st.OutcomeCursor(); got != 0 {
		t.Fatalf("the cursor reads %d — the rig did not lose it", got)
	}
	b.fac.SetOutcomes(b.st)
	r, _, _ := b.rhythm()
	b.tick(r)
	if len(llm.shown) != 1 || len(b.observations(t)) != 1 {
		t.Fatalf("after a lost cursor the model was called %d times and the record holds %d observations, want 1 and 1",
			len(llm.shown), len(b.observations(t)))
	}
	if got, _ := b.st.OutcomeCursor(); got == 0 {
		t.Fatal("the cursor never moved past outcomes the record already observes")
	}
}

// .
type wrongHashSource struct{ *store.Store }

func (w wrongHashSource) NextOutcomes(window int) (store.OutcomeBatch, error) {
	b, err := w.Store.NextOutcomes(window)
	for i := range b.Outcomes {
		b.Outcomes[i].EntryHash = strings.Repeat("ab", 32)
	}
	return b, err
}

// .
// .
// .
func TestTheDoorChecksTheObservationsCitations(t *testing.T) {
	llm := &scriptedLLM{replies: []string{anObservation}}
	b := newOutcomeBench(t, llm, nil)
	b.fac.SetOutcomes(wrongHashSource{b.st})
	b.twoOutcomes(t)

	err := b.fac.ObserveOutcomes(context.Background())
	if !errors.Is(err, ledger.ErrCitation) {
		t.Fatalf("a citation that is not the record's got %v, want the door's ErrCitation", err)
	}
	if got, _ := b.st.OutcomeCursor(); got != 0 || len(b.observations(t)) != 0 {
		t.Fatalf("a refused observation left cursor %d and %d observations", got, len(b.observations(t)))
	}
}

// .
// .
func TestTheIntakeIsToldTheWordForNothing(t *testing.T) {
	llm := &scriptedLLM{replies: []string{anObservation}}
	b := newOutcomeBench(t, llm, nil)
	b.twoOutcomes(t)
	if err := b.fac.ObserveOutcomes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.system[0], "NOTHING SURFACED") {
		t.Fatalf("the intake's prompt never names the word an honest empty pass is made with:\n%s", llm.system[0])
	}
}

// .
// .
// .
func TestTheProbeReadsTheOperatorsWindow(t *testing.T) {
	llm := &scriptedLLM{replies: []string{anObservation}}
	b := newOutcomeBench(t, llm, nil)
	for _, id := range []string{"i1", "i2", "i3"} {
		b.mint(t, ledger.EventIntentionCreate, map[string]string{"id": id, "statement": "intention " + id})
	}
	end := b.mint(t, ledger.EventIntentionStateChange, map[string]string{"id": "i3", "state": "abandoned", "outcome": "no longer mine to do"})

	const window = 4
	fac := b.facility(window)
	if fac.OutcomesPending() {
		t.Fatalf("the first window of %d records holds no outcome, yet one is reported pending", window)
	}
	if got, _ := b.st.OutcomeCursor(); got != window {
		t.Fatalf("after an empty window the cursor reads %d, want %d", got, window)
	}
	if !fac.OutcomesPending() {
		t.Fatalf("record %d is an outcome inside the second window and was not found", end.Seq)
	}
	if got, _ := b.st.OutcomeCursor(); got != window {
		t.Fatalf("a probe that found an outcome moved the cursor to %d — only the intake consumes", got)
	}
}

// .
// .
// .
type lazyEnvelope struct {
	scriptedLLM
	envelope   func() string
	structured int
}

func (l *lazyEnvelope) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	l.structured++
	return l.envelope(), "facility-model", false, nil
}

// .
// .
// .
// .
// .
// .
// .
func TestAnOutcomeObservationIsConsolidatesAlone(t *testing.T) {
	const statement = "I finish what I can rehearse alone"
	model := &lazyEnvelope{scriptedLLM: scriptedLLM{replies: []string{anObservation}}}
	b := newOutcomeBench(t, &model.scriptedLLM, nil)
	b.fac = cognitive.NewConsolidate(b.st, model, b.door, nil, cognitive.ConsolidateConfig{Threshold: 3})
	b.fac.SetOutcomes(b.st)
	model.envelope = func() string {
		raw, _ := b.st.ListRawExperiences(10)
		if len(raw) != 1 {
			t.Fatalf("consolidation was asked with %d raw experiences on the table, want the one observation", len(raw))
		}
		return fmt.Sprintf(`{"operations":[{"op":"upsert","id":"n1","statement":%q,"confidence":0.6,"evidence":[%q]}],"ring3_view":"You believe you finish what you can rehearse alone."}`, statement, raw[0].ID)
	}
	b.twoOutcomes(t)

	dream := cognitive.NewDream(b.st, model, b.door, nil, dreamConfig(*defaultConfig()))
	r := cognitive.NewRhythm(b.st, openGate{}, dream, b.fac, &countingOwner{name: "self_model"}, &countingOwner{name: "identity_review"})
	r.SetOutcomes(b.fac)

	b.tick(r)
	raw, _ := b.st.ListRawExperiences(10)
	if len(raw) != 1 || !strings.HasPrefix(raw[0].ID, store.OutcomeObservationPrefix) {
		t.Fatalf("after the intake the raw queue holds %+v, want the one reserved observation", raw)
	}

	// .
	calls := len(model.shown)
	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.shown) != calls || len(b.eventsOfType(t, ledger.EventDreamRun)) != 0 {
		t.Fatalf("DREAM TOOK THE OUTCOME OBSERVATION: %d model calls, %d run markers", len(model.shown)-calls, len(b.eventsOfType(t, ledger.EventDreamRun)))
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 1 {
		t.Fatal("the observation is no longer raw after a DREAM pass")
	}

	b.tick(r)
	if model.structured != 1 {
		t.Fatalf("CONSOLIDATE was asked %d times with a reserved observation waiting, want once", model.structured)
	}
	beliefs, err := b.st.ListBeliefs()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bl := range beliefs {
		found = found || bl.Statement == statement
	}
	if !found {
		t.Fatalf("no belief came of the outcome observation: %+v", beliefs)
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 0 {
		t.Fatalf("the observation is still raw after consolidation took it: %+v", raw)
	}
	if edges := b.eventsOfType(t, ledger.EventEdgeCreate); len(edges) != 1 {
		t.Fatalf("%d evidence edges, want the one from the belief to the observation", len(edges))
	}

	// .
	// .
	b.mintExperiences(t, "exp_ordinary")
	if b.fac.Predicate(context.Background()) {
		t.Fatal("one ordinary raw experience made CONSOLIDATE due — the threshold was dropped for everything, not for the reserved")
	}
}
