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

// The outcome intake, end to end: the REAL store, the real ledger, the
// production door (preflight → sign+append → materialize) and RHYTHM's
// own dispatch. Only the model is scripted.

// scriptedLLM answers each facility call from a script and keeps what it
// was shown.
type scriptedLLM struct {
	replies []string
	errs    []error
	shown   []string // user messages, in order
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

// ChatStructured: the intake speaks plain text; a structured call here
// would be another facility's, and the script has none.
func (s *scriptedLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	return "", "", false, errors.New("the outcome intake makes no structured call")
}

// countingOwner stands where DREAM and CONSOLIDATE stand in RHYTHM: the
// raw path, counted.
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

// outcomeBench is a cognitionBench born with a fingerprint (nothing is
// "self" without one) and someone to make promises to.
type outcomeBench struct {
	*cognitionBench
	llm *scriptedLLM
	fac *cognitive.ConsolidateFacility
}

func newOutcomeBench(t *testing.T, llm *scriptedLLM, src cognitive.OutcomeSource) *outcomeBench {
	t.Helper()
	b := newCognitionBench(t)
	evt, err := b.lg.Append(ledger.EventRing0Genesis, 0,
		map[string]string{"name": "Outcomes", "fingerprint": b.kp.Fingerprint()}, b.kp)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.st.Materialize(evt); err != nil {
		t.Fatal(err)
	}
	ob := &outcomeBench{cognitionBench: b, llm: llm}
	ob.mint(t, ledger.EventRelationshipUpsert, map[string]string{
		"id": "mira", "counterpart_name": "Mira", "counterpart_role": "peer", "relationship_type": "peer"})
	ob.fac = ob.facility(benchWindow)
	if src != nil {
		ob.fac.SetOutcomes(src)
	}
	return ob
}

// facility builds the intake's facility from the operator's numbers,
// through the function the boot builds its config with — so the wiring of
// each number is under test here.
func (b *outcomeBench) facility(window int) *cognitive.ConsolidateFacility {
	cfg := *defaultConfig()
	cfg.Prompt.SurfacingMaxChars = benchObservationBound
	cfg.Agency.OutcomeWindow = window
	fac := cognitive.NewConsolidate(b.st, b.llm, b.door, nil, consolidateConfig(cfg))
	fac.SetOutcomes(b.st)
	return fac
}

func (b *outcomeBench) mint(t *testing.T, typ ledger.EventType, payload any) *ledger.Event {
	t.Helper()
	evt, err := b.door.Append(typ, ledger.CanonicalRings(typ)[0], payload, "")
	if err != nil {
		t.Fatalf("mint %s: %v", typ, err)
	}
	return evt
}

// rhythm is a fresh RHYTHM over the bench — a fresh process, as far as
// the spacing stamps go.
func (b *outcomeBench) rhythm() (*cognitive.Rhythm, *countingOwner, *countingOwner) {
	dream, consolidate := &countingOwner{name: "dream"}, &countingOwner{name: "consolidate"}
	r := cognitive.NewRhythm(b.st, openGate{}, dream, consolidate, &countingOwner{name: "self_model"}, &countingOwner{name: "identity_review"})
	r.SetOutcomes(b.fac)
	return r, dream, consolidate
}

func (b *outcomeBench) tick(r *cognitive.Rhythm) {
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
}

// twoOutcomes: an intention completed and a commitment abandoned.
func (b *outcomeBench) twoOutcomes(t *testing.T) (done, dropped *ledger.Event) {
	t.Helper()
	b.mint(t, ledger.EventIntentionCreate, map[string]string{"id": "i1", "statement": "learn the recovery drill by heart"})
	b.mint(t, ledger.EventCommitmentPromised, map[string]string{"id": "c1", "description": "send Mira the escrow receipt", "counterpart_id": "mira"})
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

// benchObservationBound is the operator's prompt.surfacing_max_chars on
// the bench: far under the code's fallback, so a bound that is not wired
// from config shows as an over-long observation accepted.
const benchObservationBound = 200

// benchWindow is the operator's agency.outcome_window on the bench: wide
// enough that every test's outcomes stand in the first window.
const benchWindow = 64

const anObservation = "I finish what I can rehearse alone and drop what waits on a host I do not control."

// An outcome becomes ONE observation, in the identity's own record, citing
// every outcome it was shown by the exact tuple; it is raw, it mints no
// belief, and the cursor moves only after it landed.
func TestOutcomesBecomeOneCitedRawObservation(t *testing.T) {
	llm := &scriptedLLM{replies: []string{anObservation}}
	b := newOutcomeBench(t, llm, nil)
	done, dropped := b.twoOutcomes(t)

	r, dream, consolidate := b.rhythm()
	b.tick(r)

	if len(llm.shown) != 1 {
		t.Fatalf("the model was called %d times, want once", len(llm.shown))
	}
	if n := strings.Count(llm.shown[0], "(self-reported, unverified)"); n != 2 {
		t.Fatalf("each outcome must retain its qualification, got %d for two outcomes:\n%s", n, llm.shown[0])
	}
	for _, want := range []string{"learn the recovery drill by heart", "ran it three times without the page",
		"send Mira the escrow receipt", "repair: owed", "the host was down all week", "completed", "abandoned"} {
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
	// Raw: it waits in the queue every other experience waits in.
	raw, err := b.st.ListRawExperiences(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw[0].ID != o.ID {
		t.Fatalf("the raw queue holds %+v, want the observation", raw)
	}
	// It mints no belief: one road into working truth, not two.
	if n := len(b.eventsOfType(t, ledger.EventBeliefUpsert)); n != 0 {
		t.Fatalf("the intake minted %d beliefs", n)
	}
	if got, _ := b.st.OutcomeCursor(); got != dropped.Seq {
		t.Fatalf("the cursor reads %d, want %d", got, dropped.Seq)
	}
	if dream.runs != 0 || consolidate.runs != 0 {
		t.Fatalf("the raw path also ran on the intake's delta: dream=%d consolidate=%d", dream.runs, consolidate.runs)
	}

	// A restart observes nothing twice, and the next delta is the raw
	// path's: the observation is there for it to weigh.
	r2, _, consolidate2 := b.rhythm()
	b.tick(r2)
	if len(llm.shown) != 1 || len(b.observations(t)) != 1 {
		t.Fatalf("after a restart the model was called %d times and the record holds %d observations", len(llm.shown), len(b.observations(t)))
	}
	if consolidate2.runs != 1 {
		t.Fatalf("the observation waits raw and consolidation ran %d times", consolidate2.runs)
	}
}

// A failed call publishes nothing: the outcomes wait, and the next process
// to look finds them.
func TestAFailedIntakePublishesNothing(t *testing.T) {
	llm := &scriptedLLM{errs: []error{errors.New("provider down")}, replies: []string{"", anObservation}}
	b := newOutcomeBench(t, llm, nil)
	b.twoOutcomes(t)

	r, _, _ := b.rhythm()
	b.tick(r)
	b.tick(r) // inside the spacing: not tried again
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

// An honest nothing is a result: the cursor moves, the record gains
// nothing, and the same outcomes are not put to the model again.
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

// No reply is not an honest nothing: nothing was said, so nothing is
// consumed. The door holds it — an experience with no text records nothing.
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

// Held at its producer like a dream note: over the bound it is refused
// whole, and a refused observation publishes nothing.
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
	// At the bound exactly, in code points, it passes.
	llm.replies = append(llm.replies, strings.Repeat("é", benchObservationBound))
	if err := b.fac.ObserveOutcomes(context.Background()); err != nil {
		t.Fatalf("an observation at the bound was refused: %v", err)
	}
}

// losingSource is the store with a cursor that cannot be published — the
// crash between the product and the cursor.
type losingSource struct{ *store.Store }

func (losingSource) PublishOutcomeCursor(from, through uint64) error {
	return errors.New("the process died here")
}

// The product lands before the cursor. If the cursor is then lost, the
// RECORD says the outcomes were observed: no second observation of one
// outcome, which consolidation would count as a second piece of evidence.
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

func TestAPassedOverOutcomeIsNotAskedTwiceAfterALostCursor(t *testing.T) {
	llm := &scriptedLLM{replies: []string{strings.Repeat("é", benchObservationBound+1)}}
	b := newOutcomeBench(t, llm, nil)
	b.fac.SetOutcomes(losingSource{b.st})
	b.mint(t, ledger.EventIntentionCreate, map[string]string{"id": "i1", "statement": "learn the recovery drill by heart"})
	done := b.mint(t, ledger.EventIntentionStateChange, map[string]string{"id": "i1", "state": "completed", "outcome": "ran it three times without the page"})

	if err := b.fac.ObserveOutcomes(context.Background()); err != nil {
		t.Fatalf("an outcome passed over, its telling in the record, is a pass that closed: %v", err)
	}
	told := b.observations(t)
	if len(told) != 1 || told[0].Provenance != "system" || len(told[0].Cites) != 1 ||
		told[0].Cites[0] != (ledger.Citation{Identity: b.kp.Fingerprint(), Seq: done.Seq, EntryHash: done.EntryHash()}) {
		t.Fatalf("the telling of a passed-over outcome cites that outcome by its exact tuple: %+v", told)
	}
	if got, _ := b.st.OutcomeCursor(); got != 0 {
		t.Fatalf("the cursor reads %d — the rig did not lose it", got)
	}
	b.fac.SetOutcomes(b.st)
	r, _, _ := b.rhythm()
	b.tick(r)
	if len(llm.shown) != 1 || len(b.observations(t)) != 1 {
		t.Fatalf("A PASSED-OVER OUTCOME WAS ASKED AGAIN: after a lost cursor the model was called %d times and the record holds %d tellings, want 1 and 1",
			len(llm.shown), len(b.observations(t)))
	}
	if got, _ := b.st.OutcomeCursor(); got == 0 {
		t.Fatal("the cursor never moved past an outcome the record says was passed over")
	}
}

// wrongHashSource hands the intake a tuple that is not the record's.
type wrongHashSource struct{ *store.Store }

func (w wrongHashSource) NextOutcomes(window int) (store.OutcomeBatch, error) {
	b, err := w.Store.NextOutcomes(window)
	for i := range b.Outcomes {
		b.Outcomes[i].EntryHash = strings.Repeat("ab", 32)
	}
	return b, err
}

// The observation goes through the ONE door, and the door checks a
// citation of the identity's own record against that record. A tuple that
// is not the record's is refused there — and a refusal publishes nothing.
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

// The intake speaks under the identity's authority rings like every other
// facility, and is told the exact word for nothing.
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

// The probe reads the operator's window and no more: a window that holds
// no outcome is moved past, and the outcome beyond it is found on a later
// tick — nothing skipped, nothing read twice.
func TestTheProbeReadsTheOperatorsWindow(t *testing.T) {
	llm := &scriptedLLM{replies: []string{anObservation}}
	b := newOutcomeBench(t, llm, nil) // records 1-2: the birth and the relationship
	for _, id := range []string{"i1", "i2", "i3"} {
		b.mint(t, ledger.EventIntentionCreate, map[string]string{"id": id, "statement": "intention " + id})
	} // records 3-5
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

// lazyEnvelope answers the intake in plain text and consolidation with an
// envelope built when it is asked for — the observation's id is derived
// from its content and is not known before the intake ran.
type lazyEnvelope struct {
	scriptedLLM
	envelope   func() string
	structured int
}

func (l *lazyEnvelope) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	l.structured++
	return l.envelope(), "facility-model", false, nil
}

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

	b.tick(r) // the intake: one raw observation
	raw, _ := b.st.ListRawExperiences(10)
	if len(raw) != 1 || !strings.HasPrefix(raw[0].ID, store.OutcomeObservationPrefix) {
		t.Fatalf("after the intake the raw queue holds %+v, want the one reserved observation", raw)
	}

	// DREAM, asked directly, reads nothing and spends nothing: it is not DREAM's.
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

	b.tick(r) // CONSOLIDATE's delta: due on the observation alone, under its threshold of three
	if model.structured != 1 {
		t.Fatalf("CONSOLIDATE was asked %d times with a reserved observation waiting, want once", model.structured)
	}
	beliefs, err := b.st.ListBeliefs()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var beliefID string
	for _, bl := range beliefs {
		if bl.Statement == statement {
			found, beliefID = true, bl.ID
		}
	}
	if !found {
		t.Fatalf("no belief came of the outcome observation: %+v", beliefs)
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 0 {
		t.Fatalf("the observation is still raw after consolidation took it: %+v", raw)
	}
	if edges, err := b.st.ListEdgesForBelief(beliefID); err != nil || len(edges) != 1 || edges[0].ToID != beliefID {
		t.Fatalf("want one incoming source edge from the observation: %+v %v", edges, err)
	}

	// And the threshold still means what it meant for everything else: one
	// ordinary note is not due.
	b.mintExperiences(t, "exp_ordinary")
	if b.fac.Predicate(context.Background()) {
		t.Fatal("one ordinary raw experience made CONSOLIDATE due — the threshold was dropped for everything, not for the reserved")
	}
}
