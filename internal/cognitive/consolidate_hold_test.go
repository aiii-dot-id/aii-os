package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// holdRig is DREAM and CONSOLIDATE as the boot wires them: n experiences
// wait, e1 the oldest, and DREAM is handed what CONSOLIDATE holds.
// CONSOLIDATE's model finishes a reply over at most finishes experiences;
// asked for more, its reply is cut off at the output limit. The last
// return is DREAM's model.
func holdRig(n, finishes int, cfg ConsolidateConfig) (*mockStore, *mockLedger, *ConsolidateFacility, *DreamFacility, *mockLLM) {
	st := &mockStore{unprocessedCnt: n}
	for i := n; i >= 1; i-- { // newest first, as the store lists them; the mock's queue is oldest first
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d, a thing that happened", i), Raw: 1})
	}
	lg := &mockLedger{st: st}
	consolidating := &sizedModel{reply: func(user string) (string, error) {
		if strings.Count(user, "\n- [") > finishes {
			return "", &wire.IncompleteResponseError{Reason: "length", Got: 8000}
		}
		return upsertOn(user)
	}}
	c := NewConsolidate(st, consolidating, lg, &mockRingWriter{}, cfg)
	dreaming := &mockLLM{override: "You notice a pattern in what happened."}
	d := NewDream(st, dreaming, lg, &mockRingWriter{}, DreamConfig{})
	d.SetHeld(c)
	return st, lg, c, d, dreaming
}

// runInputs is what each run marker of one kind consumed, in the order the
// markers landed.
func runInputs(lg *mockLedger, marker ledger.EventType) [][]string {
	var out [][]string
	for i, et := range lg.appended {
		if et != marker {
			continue
		}
		if p, ok := lg.payloads[i].(store.FacilityRunPayload); ok {
			out = append(out, p.Inputs)
		}
	}
	return out
}

// A BATCH CONSOLIDATION WAS REFUSED STAYS ITS OWN UNTIL A PASS CLOSES. A
// pass refused for what it asked said "the next pass reads half" and
// consumed nothing. The tick after was DREAM's: it read the same
// experiences, oldest first, and consumed them, and the smaller pass
// CONSOLIDATE had announced found them gone. Which facility took an
// experience was decided by where the timer stood.
//
// Here six experiences wait, a consolidation pass reads four, and the model
// finishes a reply over two. The pass of four is refused. DREAM's tick
// reads the two that pass did not try and none of the four it did; its next
// tick has nothing of its own, declines and asks the model nothing.
// CONSOLIDATE's next pass reads the two it said it would, and closes. A
// pass has closed, so nothing is held, and DREAM reads what is left. Each
// experience is consumed once.
func TestABatchConsolidationWasRefusedStaysItsOwnUntilAPassCloses(t *testing.T) {
	ctx := context.Background()
	st, lg, c, dream, dreaming := holdRig(6, 2, ConsolidateConfig{Batch: 4})
	tick := func() AlarmResult { return dream.OnAlarm(ctx, "rhythm:dream", "wall", 0, "") }

	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if next := c.nextBatch(c.cfg()); next != 2 || len(lg.runPayloads()) != 0 || st.unprocessedCnt != 6 {
		t.Fatalf("rig: a pass of four is refused, consumes nothing, and the next reads two: next %d, %d run(s), %d waiting", next, len(lg.runPayloads()), st.unprocessedCnt)
	}

	if res := tick(); !res.Accepted || dreaming.calls != 1 {
		t.Fatalf("rig: two experiences are DREAM's own to read: accepted %v, %d call(s)", res.Accepted, dreaming.calls)
	}
	for i := 1; i <= 4; i++ {
		if strings.Contains(dreaming.lastUser, fmt.Sprintf("NOTED-e%d,", i)) {
			t.Fatalf("DREAM READ WHAT CONSOLIDATION SAID ITS NEXT PASS WOULD: e%d is in the dream request:\n%s", i, dreaming.lastUser)
		}
	}
	if got := fmt.Sprint(runInputs(lg, ledger.EventDreamRun)); got != "[[e5 e6]]" {
		t.Fatalf("DREAM CONSUMED A BATCH CONSOLIDATION WAS REFUSED: its run markers consumed %s; want [[e5 e6]], the two that pass never tried", got)
	}
	if res := tick(); res.Accepted || dreaming.calls != 1 || st.unprocessedCnt != 4 {
		t.Fatalf("with nothing of its own waiting DREAM declines, asks nothing and consumes nothing: accepted %v, %d call(s), %d waiting", res.Accepted, dreaming.calls, st.unprocessedCnt)
	}

	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(runInputs(lg, ledger.EventConsolidationRun)); got != "[[e1 e2]]" {
		t.Fatalf("consolidation's next pass reads the two it said it would, and closes: its run markers consumed %s; want [[e1 e2]]", got)
	}

	// A pass closed: nothing is held, and what is left is read by whichever
	// facility the timer gives it to, as before.
	if held, err := c.HeldForRetry(); err != nil || len(held) != 0 {
		t.Fatalf("after a pass that closed consolidation holds nothing: %v (%v)", held, err)
	}
	if res := tick(); !res.Accepted {
		t.Fatal("DREAM reads what is left once nothing is held")
	}
	seen := map[string]int{}
	for _, run := range lg.runPayloads() {
		for _, id := range run.Inputs {
			seen[id]++
		}
	}
	for i := 1; i <= 6; i++ {
		if id := fmt.Sprintf("e%d", i); seen[id] != 1 {
			t.Fatalf("every experience is consumed once, by one facility: %s was consumed %d time(s) (%v)", id, seen[id], lg.runPayloads())
		}
	}
	if st.unprocessedCnt != 0 {
		t.Fatalf("%d experiences still wait", st.unprocessedCnt)
	}
}

// A BATCH CONSOLIDATION CAN NEVER CLOSE DOES NOT KEEP ITS EXPERIENCES FROM
// DREAM FOR GOOD. What ends the wait is what the facility already does with
// a refusal that will not go away: half, then half again, and one
// experience refused alone is set aside, with the identity told "A dream
// pass may still read it." Set aside, it is out of CONSOLIDATE's queue, so
// out of what it holds.
//
// Here three experiences wait and the model finishes no reply. RHYTHM's two
// modes take turns. The pass of three is refused, and DREAM reads none of
// them. Then each pass of one is refused and sets its experience aside, and
// the tick after it is the first at which DREAM reads that experience, and
// reads only that one. In four rounds all three are DREAM's, each consumed
// once.
func TestABatchConsolidationCanNeverCloseIsReleasedToDreamAsEachIsSetAside(t *testing.T) {
	ctx := context.Background()
	st, lg, c, dream, dreaming := holdRig(3, 0, ConsolidateConfig{Threshold: 1})
	round := func() {
		t.Helper()
		if err := c.Execute(ctx); err != nil {
			t.Fatal(err)
		}
		dream.OnAlarm(ctx, "rhythm:dream", "wall", 0, "")
	}

	round()
	if c.nextBatch(c.cfg()) != 1 || c.aside.count() != 0 {
		t.Fatalf("rig: the pass of three is refused and the next reads one: next %d, %d set aside", c.nextBatch(c.cfg()), c.aside.count())
	}
	if dreaming.calls != 0 || st.unprocessedCnt != 3 {
		t.Fatalf("DREAM READ A BATCH CONSOLIDATION WAS STILL HALVING: %d dream call(s), %d of three still waiting", dreaming.calls, st.unprocessedCnt)
	}
	for i := 1; i <= 3; i++ {
		round()
		id := fmt.Sprintf("e%d", i)
		if c.aside.count() != i || !c.aside.has(id) {
			t.Fatalf("rig: round %d refuses %s alone and sets it aside: %d set aside", i+1, id, c.aside.count())
		}
		runs := runInputs(lg, ledger.EventDreamRun)
		if len(runs) != i || fmt.Sprint(runs[i-1]) != "["+id+"]" {
			t.Fatalf("AN EXPERIENCE SET ASIDE WAS NOT RELEASED TO DREAM, OR DREAM READ ONE STILL HELD: after round %d DREAM's run markers consumed %v; want %s alone in the last, the one just set aside", i+1, runs, id)
		}
	}
	if n := len(runInputs(lg, ledger.EventConsolidationRun)); n != 0 || st.unprocessedCnt != 0 || dreaming.calls != 3 {
		t.Fatalf("all three end as DREAM's, each read once: %d consolidation run(s), %d still waiting, %d dream call(s)", n, st.unprocessedCnt, dreaming.calls)
	}
	told := toldIdentity(lg)
	if len(told) != 3 {
		t.Fatalf("the identity is told once of each experience set aside: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "A dream pass may still read it.") {
			t.Fatalf("what the identity is told of an experience set aside says a dream pass may read it, and now one does: %q", content)
		}
	}
}

// CONSOLIDATION HOLDS NOTHING IT WILL NOT ASK FOR. Under its threshold of
// three CONSOLIDATE asks the model for nothing; held then, what waits
// would wait on a facility that was not coming for it at this tick.
//
// Here three experiences wait and the model finishes no reply. The pass of
// three is refused, the pass of one is refused and sets its experience
// aside, and DREAM reads that one. Two wait now, under the threshold:
// CONSOLIDATE is not due, holds nothing though its last pass was refused,
// and DREAM's next tick reads them, before consolidation's next pass.
func TestConsolidationHoldsNothingItWillNotAskFor(t *testing.T) {
	ctx := context.Background()
	st, lg, c, dream, _ := holdRig(3, 0, ConsolidateConfig{})
	tick := func() AlarmResult { return dream.OnAlarm(ctx, "rhythm:dream", "wall", 0, "") }
	for pass := 0; pass < 2; pass++ {
		if err := c.Execute(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if held, err := c.HeldForRetry(); err != nil || fmt.Sprint(held) != "[e2 e3]" || !c.aside.has("e1") {
		t.Fatalf("rig: after the pass of three and the pass of one are refused, e1 is set aside and consolidation holds the other two: %v (%v), %d set aside", held, err, c.aside.count())
	}
	if res := tick(); !res.Accepted || fmt.Sprint(runInputs(lg, ledger.EventDreamRun)) != "[[e1]]" {
		t.Fatalf("rig: DREAM reads the one set aside and no other: accepted %v, dream runs %v", res.Accepted, runInputs(lg, ledger.EventDreamRun))
	}
	if c.Predicate(ctx) {
		t.Fatal("rig: two experiences are under the threshold of three, and consolidation is not due")
	}
	if held, err := c.HeldForRetry(); err != nil || len(held) != 0 {
		t.Fatalf("CONSOLIDATION HELD WHAT IT WILL NOT ASK FOR: %v (%v)", held, err)
	}
	if res := tick(); !res.Accepted || st.unprocessedCnt != 0 {
		t.Fatalf("DREAM reads what consolidation is not due for: accepted %v, %d still waiting", res.Accepted, st.unprocessedCnt)
	}
	if got := fmt.Sprint(runInputs(lg, ledger.EventDreamRun)); got != "[[e1] [e2 e3]]" {
		t.Fatalf("DREAM's run markers consumed %s; want [[e1] [e2 e3]]", got)
	}
}

// A REFUSAL FOLLOWED BY A SMALLER REFUSAL KEEPS THE HOLD. The ladder is
// being climbed down: each pass asks for less of the same experiences, and
// none of them is DREAM's while it is.
//
// Here five experiences wait, a pass reads four and the model finishes no
// reply. The pass of four is refused, then the pass of two. After each,
// consolidation holds the four, and DREAM reads the fifth once and nothing
// after it.
func TestARefusalFollowedByASmallerRefusalKeepsTheHold(t *testing.T) {
	ctx := context.Background()
	st, lg, c, dream, dreaming := holdRig(5, 0, ConsolidateConfig{Batch: 4})
	for pass, next := range []int{2, 1} {
		if err := c.Execute(ctx); err != nil {
			t.Fatal(err)
		}
		if got := c.nextBatch(c.cfg()); got != next {
			t.Fatalf("rig: refused pass %d is followed by one of %d experiences, not %d", pass+1, next, got)
		}
		if held, err := c.HeldForRetry(); err != nil || fmt.Sprint(held) != "[e1 e2 e3 e4]" {
			t.Fatalf("A SMALLER REFUSAL LET GO OF THE BATCH: after refused pass %d consolidation holds %v (%v); want the four it has tried", pass+1, held, err)
		}
		dream.OnAlarm(ctx, "rhythm:dream", "wall", 0, "")
		if got := fmt.Sprint(runInputs(lg, ledger.EventDreamRun)); got != "[[e5]]" || dreaming.calls != 1 || st.unprocessedCnt != 4 {
			t.Fatalf("DREAM READ A BATCH CONSOLIDATION WAS STILL HALVING: after refused pass %d its run markers consumed %s in %d call(s), %d waiting; want [[e5]] in one call and four waiting", pass+1, got, dreaming.calls, st.unprocessedCnt)
		}
	}
}

// A PASS THAT CLOSES ENDS THE HOLD FOR WHAT IT DID NOT READ: "until a pass
// closes", to the letter. Where the other closes of this file leave two
// experiences, consolidation is under its threshold and would hold nothing
// whatever its last pass came to. Here it is still due after the close, so
// nothing but the close released what waits.
//
// Thirty experiences wait, the operator's batch is the default thirty, and
// the model finishes a reply over fifteen. The pass of thirty is refused,
// consolidation holds the thirty and DREAM's tick reads none. The pass of
// fifteen closes. Fifteen wait and consolidation is due; nothing is held,
// and DREAM's next tick reads the fifteen the refused pass read and the
// closing pass did not.
func TestAPassThatClosesReleasesWhatItDidNotRead(t *testing.T) {
	ctx := context.Background()
	st, lg, c, dream, dreaming := holdRig(30, 15, ConsolidateConfig{})
	tick := func() AlarmResult { return dream.OnAlarm(ctx, "rhythm:dream", "wall", 0, "") }
	ids := func(from, to int) string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, fmt.Sprintf("e%d", i))
		}
		return fmt.Sprint(out)
	}

	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	held, err := c.HeldForRetry()
	if res := tick(); err != nil || fmt.Sprint(held) != ids(1, 30) || c.nextBatch(c.cfg()) != 15 || res.Accepted || dreaming.calls != 0 {
		t.Fatalf("rig: the pass of thirty is refused, the next reads fifteen, consolidation holds the thirty and DREAM reads none: held %v (%v), next %d, dream accepted %v with %d call(s)", held, err, c.nextBatch(c.cfg()), res.Accepted, dreaming.calls)
	}

	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(runInputs(lg, ledger.EventConsolidationRun)); got != "["+ids(1, 15)+"]" || st.unprocessedCnt != 15 || !c.Predicate(ctx) {
		t.Fatalf("rig: the pass of fifteen closes over the oldest fifteen, and with fifteen waiting consolidation is still due: its run markers consumed %s, %d waiting, due %v", got, st.unprocessedCnt, c.Predicate(ctx))
	}
	if held, err := c.HeldForRetry(); err != nil || len(held) != 0 {
		t.Fatalf("A PASS THAT CLOSED STILL HOLDS FROM DREAM WHAT IT DID NOT READ: %v (%v)", held, err)
	}
	if res := tick(); !res.Accepted || fmt.Sprint(runInputs(lg, ledger.EventDreamRun)) != "["+ids(16, 30)+"]" || st.unprocessedCnt != 0 {
		t.Fatalf("DREAM's next tick reads the fifteen the closing pass did not: accepted %v, its run markers consumed %v, %d waiting", res.Accepted, runInputs(lg, ledger.EventDreamRun), st.unprocessedCnt)
	}
}

// exitModel is CONSOLIDATE's model for a pass that ends neither closed nor
// refused for what it asked: reply answers the structured call, and admit
// is what the client's check says of every request before it is sent (nil:
// admitted). A test changes either between passes.
type exitModel struct {
	reply func(user string) (string, error)
	admit error
}

func (m *exitModel) ChatSimple(context.Context, string, string) (string, string, error) {
	return "", "", errors.New("exitModel answers no plain call")
}

func (m *exitModel) ChatStructured(_ context.Context, _, user string, _ wire.ToolDefinition) (string, string, bool, error) {
	text, err := m.reply(user)
	return text, "exit-model", false, err
}

func (m *exitModel) CheckSimple(context.Context, string, string) error { return m.admit }
func (m *exitModel) CheckWithTools(context.Context, string, string, []wire.ToolDefinition) error {
	return m.admit
}

// A PASS THAT ENDS OFF THE LADDER RELEASES THE HOLD. A refusal's smaller
// batch stands until passes have closed, and a pass can end five ways that
// neither close nor are a refusal of what was asked: no room, a check that
// failed, a failed call, a fault while minting, a run marker refused. None
// moves the batch, so held on the batch alone the head of the queue was
// denied to DREAM at every tick for as long as consolidation went on
// ending that way: a provider that takes plain chat and refuses a request
// offering a tool, or rings that leave a new model no room, for the life of
// the process.
//
// Here four experiences wait. The first pass is refused for a reply cut
// off, consolidation holds the four, and DREAM's tick reads none. The next
// pass ends one of the five ways. The smaller batch is as the refusal left
// it, nothing is held, and DREAM's next tick reads the four. Each way is
// its own subtest, so a fault in the release shows at every way it reaches.
func TestAPassThatEndsOffTheLadderReleasesTheHold(t *testing.T) {
	ctx := context.Background()
	cutOff := func(string) (string, error) {
		return "", &wire.IncompleteResponseError{Reason: "length", Got: 8000}
	}
	failed := errors.New("the client's profile does not validate")
	exits := []struct {
		name    string
		arrange func(st *mockStore, lg *mockLedger, m *exitModel)
		err     error // what the pass returns
	}{
		{name: "no room", arrange: func(_ *mockStore, _ *mockLedger, m *exitModel) {
			m.admit = &wire.ContextLimitError{Required: 2, Limit: 1}
		}},
		{name: "a check that failed", err: failed, arrange: func(_ *mockStore, _ *mockLedger, m *exitModel) {
			m.admit = failed
		}},
		{name: "a failed call", arrange: func(_ *mockStore, _ *mockLedger, m *exitModel) {
			m.reply = func(string) (string, error) { return "", errors.New("provider down") }
		}},
		{name: "a fault while minting", arrange: func(st *mockStore, _ *mockLedger, m *exitModel) {
			m.reply = upsertOn
			st.beliefsErr = errors.New("database is locked")
		}},
		{name: "a run marker refused", arrange: func(_ *mockStore, lg *mockLedger, m *exitModel) {
			m.reply = upsertOn
			lg.refuse = func(et ledger.EventType) error {
				if et == ledger.EventConsolidationRun {
					return errors.New("the door refused the marker")
				}
				return nil
			}
		}},
	}
	for _, exit := range exits {
		t.Run(exit.name, func(t *testing.T) {
			st := &mockStore{unprocessedCnt: 4}
			for i := 4; i >= 1; i-- {
				st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d, a thing that happened", i), Raw: 1})
			}
			lg := &mockLedger{st: st}
			model := &exitModel{reply: cutOff}
			c := NewConsolidate(st, model, lg, &mockRingWriter{}, ConsolidateConfig{})
			dreaming := &mockLLM{override: "You notice a pattern in what happened."}
			dream := NewDream(st, dreaming, lg, &mockRingWriter{}, DreamConfig{})
			dream.SetHeld(c)
			tick := func() AlarmResult { return dream.OnAlarm(ctx, "rhythm:dream", "wall", 0, "") }

			if err := c.Execute(ctx); err != nil {
				t.Fatalf("%s: %v", exit.name, err)
			}
			held, err := c.HeldForRetry()
			if res := tick(); err != nil || fmt.Sprint(held) != "[e1 e2 e3 e4]" || c.nextBatch(c.cfg()) != 2 || res.Accepted || dreaming.calls != 0 {
				t.Fatalf("%s: rig: the pass of four is refused, consolidation holds the four and DREAM reads none: held %v (%v), next %d, dream accepted %v with %d call(s)", exit.name, held, err, c.nextBatch(c.cfg()), res.Accepted, dreaming.calls)
			}

			exit.arrange(st, lg, model)
			if err := c.Execute(ctx); !errors.Is(err, exit.err) {
				t.Fatalf("%s: rig: the pass returned %v; want %v", exit.name, err, exit.err)
			}
			if n := len(runInputs(lg, ledger.EventConsolidationRun)); n != 0 || st.unprocessedCnt != 4 || c.nextBatch(c.cfg()) != 2 {
				t.Fatalf("%s: rig: the pass neither closes nor is refused for what it asked, and the smaller batch stands: %d run(s), %d waiting, next %d", exit.name, n, st.unprocessedCnt, c.nextBatch(c.cfg()))
			}
			if held, err := c.HeldForRetry(); err != nil || len(held) != 0 {
				t.Fatalf("%s: A PASS THAT ENDED OFF THE LADDER STILL HOLDS THE HEAD OF THE QUEUE FROM DREAM: %v (%v)", exit.name, held, err)
			}
			if res := tick(); !res.Accepted || fmt.Sprint(runInputs(lg, ledger.EventDreamRun)) != "[[e1 e2 e3 e4]]" {
				t.Fatalf("%s: DREAM's next tick reads what was held: accepted %v, its run markers consumed %v; want [[e1 e2 e3 e4]]", exit.name, res.Accepted, runInputs(lg, ledger.EventDreamRun))
			}
		})
	}
}

// A CHANGE OF MODEL RELEASES THE HOLD. The refusal was the last model's;
// whether the new one can read what waits is not known until it has tried,
// and it may never try.
func TestAChangeOfModelReleasesWhatConsolidationHeld(t *testing.T) {
	ctx := context.Background()
	st, lg, c, dream, _ := holdRig(4, 0, ConsolidateConfig{})
	if err := c.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if held, err := c.HeldForRetry(); err != nil || fmt.Sprint(held) != "[e1 e2 e3 e4]" {
		t.Fatalf("rig: after the refusal consolidation holds the four it tried: %v (%v)", held, err)
	}
	c.Reconsider()
	if held, err := c.HeldForRetry(); err != nil || len(held) != 0 || c.nextBatch(c.cfg()) != 2 {
		t.Fatalf("A REFUSAL BY THE LAST MODEL STILL HOLDS THE HEAD OF THE QUEUE FROM DREAM: %v (%v); the smaller batch stands at %d, want 2", held, err, c.nextBatch(c.cfg()))
	}
	if res := dream.OnAlarm(ctx, "rhythm:dream", "wall", 0, ""); !res.Accepted || st.unprocessedCnt != 0 {
		t.Fatalf("DREAM reads what was held: accepted %v, %d still waiting, its run markers consumed %v", res.Accepted, st.unprocessedCnt, runInputs(lg, ledger.EventDreamRun))
	}
}

// THE CARD SAYS WHAT A DREAM PASS LEAVES, at the end of the line
// consolidation writes for each refusal, and the words are held here: the
// Cognition card is the one place the operator is told of the hold before
// a dream pass logs it.
//
// Here three experiences wait and the model finishes no reply. The pass of
// three is refused and halved, and the pass of one is refused and sets its
// experience aside. Each of the two sentences ends with the same one.
func TestTheCardSaysWhatADreamPassLeavesAfterEachRefusal(t *testing.T) {
	ctx := context.Background()
	_, _, c, _, _ := holdRig(3, 0, ConsolidateConfig{})
	var said []Condition
	c.SetTeller(&Teller{Report: func(c Condition) {
		if c.Kind == ConditionHalved || c.Kind == ConditionSetAside {
			said = append(said, c)
		}
	}})
	for pass := 0; pass < 2; pass++ {
		if err := c.Execute(ctx); err != nil {
			t.Fatal(err)
		}
	}
	const left = "Until consolidation's next pass has run, dream passes leave it the oldest experiences still waiting for it, up to cognition.consolidate_batch of them (a change of model, or too few waiting for a pass, ends that sooner)"
	want := []struct {
		kind         ConditionKind
		begins, ends string
	}{
		{ConditionHalved, "a pass of 3 experiences was refused for what it asked (", ") — nothing minted, nothing consumed; the next pass reads 1. " + left},
		{ConditionSetAside, "experience e1 was refused alone (", ") — set aside: no consolidation pass asks for it again until the identity restarts or their model changes. " + left},
	}
	if len(said) != len(want) {
		t.Fatalf("rig: the card is told of the pass halved, then of the experience set aside: %v", said)
	}
	for i, w := range want {
		if got := said[i]; got.Facility != "consolidate" || got.Kind != w.kind || !strings.HasPrefix(got.Said, w.begins) || !strings.HasSuffix(got.Said, w.ends) {
			t.Errorf("THE CARD DOES NOT SAY WHAT A DREAM PASS LEAVES TO CONSOLIDATION, WORD FOR WORD (%s):\n got: %s\nwant: %s<why>%s", w.kind, got.Said, w.begins, w.ends)
		}
	}
}

// heldIDs is a CONSOLIDATE that holds the ids given.
type heldIDs []string

func (h heldIDs) HeldForRetry() ([]string, error) { return h, nil }

func TestDreamReadsPastABatchConsolidationHolds(t *testing.T) {
	n := dreamBatch + 3
	st := &mockStore{unprocessedCnt: n}
	for i := n; i >= 1; i-- { // as holdRig lists them: e1 the oldest
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: fmt.Sprintf("NOTED-e%d", i), Raw: 1})
	}
	d := NewDream(st, &mockLLM{}, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{})
	var held heldIDs
	for i := 1; i <= dreamBatch; i++ {
		held = append(held, fmt.Sprintf("e%d", i))
	}
	d.SetHeld(held)
	queue, err := d.waiting()
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, e := range queue {
		read = append(read, e.ID)
	}
	if want := fmt.Sprintf("[e%d e%d e%d]", n-2, n-1, n); fmt.Sprint(read) != want {
		t.Fatalf("with the %d oldest held, DREAM reads %v; want %s, the three behind them", dreamBatch, read, want)
	}
}
