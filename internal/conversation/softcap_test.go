package conversation

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const checkpointMark = "Checkpoint NOW"

// checkpointCount counts the ask in the FINAL request's conversation:
// the note persists in history once sent, so counting every request
// would count the same note again each round.
func checkpointCount(client *captureLLM) int {
	if len(client.seen) == 0 {
		return 0
	}
	last := client.seen[len(client.seen)-1]
	n := 0
	for _, m := range last {
		n += strings.Count(m.Content, checkpointMark)
	}
	return n
}

// runCapTurn scripts `toolRounds` single-call rounds followed by text
// finals, under a loop wired (or not) with a prediction accessor.
func runCapTurn(t *testing.T, toolRounds, maxIter int, predicted func() int) (*captureLLM, Result) {
	t.Helper()
	return runCheckpointTurn(t, toolRounds, maxIter, predicted, Checkpoint{})
}

// runCheckpointTurn is runCapTurn under a checkpoint the operator set.
func runCheckpointTurn(t *testing.T, toolRounds, maxIter int, predicted func() int, checkpoint Checkpoint) (*captureLLM, Result) {
	t.Helper()
	script := make([]llm.Response, 0, toolRounds+2)
	for i := 0; i < toolRounds; i++ {
		script = append(script, resp("", toolCall("c", "bash", `{}`)))
	}
	script = append(script, textResp("done"), textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
		Config{MaxIterations: maxIter, PredictedThisTurn: budgetAtStart(predicted), Checkpoint: checkpoint})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	return client, res
}

func TestTheCheckpointIsTheOperators(t *testing.T) {
	undeclared := func() int { return 0 }
	client, res := runCheckpointTurn(t, 6, 60, undeclared, Checkpoint{Calls: 5, GraceRounds: 1})
	if !res.ContinuedAtCap {
		t.Fatal("a turn past the operator's floor did not flag ContinuedAtCap")
	}
	// Arming at the end of round 5, grace 1 -> six tool requests + final.
	if got := len(client.seen); got != 7 {
		t.Errorf("turn took %d requests, want 7: the operator's floor of 5 and grace of 1 did not bound it", got)
	}
	all := joined(client.seen[len(client.seen)-1])
	if !strings.Contains(all, "spent 5 calls with no declared budget") || !strings.Contains(all, "You have 1 more round;") {
		t.Errorf("the ask does not state the checkpoint that fired:\n%s", all)
	}

	// Raised past what the turn spends, there is no checkpoint at all: the
	// 40 calls that ended at 32 run to their natural end.
	client, res = runCheckpointTurn(t, 40, 60, undeclared, Checkpoint{Calls: 1000})
	if res.ContinuedAtCap || checkpointCount(client) != 0 || len(client.seen) != 41 {
		t.Errorf("a floor of 1000 still checkpointed a 40-call turn: %d requests, %d asks, capped=%v",
			len(client.seen), checkpointCount(client), res.ContinuedAtCap)
	}

	// A declared turn keeps its own rule, twice the estimate, and takes the
	// operator's grace: 3 declared arms at call 6, grace 2 -> 8 + final.
	client, res = runCheckpointTurn(t, 8, 60, func() int { return 3 }, Checkpoint{Calls: 1000, GraceRounds: 2})
	if got := len(client.seen); got != 9 || !res.ContinuedAtCap {
		t.Errorf("a declared turn took %d requests, capped=%v; want 9 and capped (twice its 3, then a grace of 2)", got, res.ContinuedAtCap)
	}
	if all := joined(client.seen[len(client.seen)-1]); !strings.Contains(all, "you estimated 3 remaining calls and have spent 6 since that declaration") || !strings.Contains(all, "You have 2 more rounds;") {
		t.Errorf("the declared turn's ask does not state its budget and the operator's grace:\n%s", all)
	}
}

// THE SLACK ON A DECLARED ESTIMATE IS THE OPERATOR'S
// (agency.turn_checkpoint_slack). It was 2 in this package whatever the
// configuration said: a turn that declared 3 was checkpointed at 6. At a
// slack of 3 it is checkpointed at 9, and the ask says the estimate and
// the spend. An estimate so large that times the slack it passes the
// largest int is no checkpoint at all, never a negative one that fires at
// once.
func TestTheSlackIsTheOperators(t *testing.T) {
	client, res := runCheckpointTurn(t, 11, 60, func() int { return 3 }, Checkpoint{Calls: 1000, GraceRounds: 2, Slack: 3})
	if got := len(client.seen); got != 12 || !res.ContinuedAtCap {
		t.Errorf("a turn that declared 3 under a slack of 3 took %d requests, capped=%v; want 12 and capped (three times its 3, then a grace of 2)", got, res.ContinuedAtCap)
	}
	if all := joined(client.seen[len(client.seen)-1]); !strings.Contains(all, "you estimated 3 remaining calls and have spent 9 since that declaration") {
		t.Errorf("the ask does not state the estimate and the spend at a slack of 3:\n%s", all)
	}
	client, res = runCheckpointTurn(t, 5, 60, func() int { return math.MaxInt / 2 }, Checkpoint{Slack: 3})
	if res.ContinuedAtCap || checkpointCount(client) != 0 || len(client.seen) != 6 {
		t.Errorf("an estimate past the largest int times the slack checkpointed: %d requests, %d asks, capped=%v",
			len(client.seen), checkpointCount(client), res.ContinuedAtCap)
	}
}

// A number that is not positive is that number's default: zero in the
// operator's file means "take the default", for each of the two apart.
func TestACheckpointOfZeroIsTheDefault(t *testing.T) {
	for name, checkpoint := range map[string]Checkpoint{"zero": {}, "negative": {Calls: -1, GraceRounds: -1}} {
		client, res := runCheckpointTurn(t, 36, 60, func() int { return 0 }, checkpoint)
		if !res.ContinuedAtCap || len(client.seen) != 37 {
			t.Errorf("%s: %d requests, capped=%v; want the default floor of %d and grace of %d (37 requests, capped)",
				name, len(client.seen), res.ContinuedAtCap, DefaultCheckpointCalls, DefaultCheckpointGraceRounds)
		}
	}
	if got := (Checkpoint{Calls: 7}).InForce(); got != (Checkpoint{Calls: 7, GraceRounds: DefaultCheckpointGraceRounds, Slack: DefaultCheckpointSlack}) {
		t.Errorf("a floor of 7 with no grace named is %+v in force", got)
	}
	if DefaultCheckpointCalls != 32 || DefaultCheckpointGraceRounds != 4 {
		t.Errorf("the defaults are %d and %d; the loop carried 32 and 4", DefaultCheckpointCalls, DefaultCheckpointGraceRounds)
	}
}

// THE GRACE IS HELD AGAINST THE ROUNDS LEFT, never added to the round: it
// is the operator's number and may be any size. One too large to fit is no
// checkpoint, and the turn ends at its round limit as it always has
// beside one; the largest number there is does not wrap into an ask sent
// every round.
func TestAGraceThatDoesNotFitIsNoCheckpoint(t *testing.T) {
	for _, grace := range []int{7, 8, 1 << 62, int(^uint(0) >> 1)} {
		client, res := runCheckpointTurn(t, 8, 8, func() int { return 0 }, Checkpoint{Calls: 1, GraceRounds: grace})
		if res.ContinuedAtCap || !res.ExhaustedBudget || checkpointCount(client) != 0 {
			t.Errorf("grace %d beside 8 rounds: capped=%v exhausted=%v asks=%d; want the round limit's ending and no ask",
				grace, res.ContinuedAtCap, res.ExhaustedBudget, checkpointCount(client))
		}
	}
	// One below that fits in the first round and no later: armed there.
	client, res := runCheckpointTurn(t, 7, 8, func() int { return 0 }, Checkpoint{Calls: 1, GraceRounds: 6})
	if !res.ContinuedAtCap || checkpointCount(client) != 1 {
		t.Errorf("grace 6 beside 8 rounds: capped=%v asks=%d; want one ask and a capped ending", res.ContinuedAtCap, checkpointCount(client))
	}
}

// resetTools changes the loop's bounds while a turn is running: at its
// third tool call, as a reload of the configuration would.
type resetTools struct {
	fakeTools
	loop *Loop
	to   Checkpoint
}

func (r *resetTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	if len(r.calls) == 2 {
		r.loop.SetTurnBounds(60, 0, r.to)
	}
	return r.fakeTools.Execute(ctx, call)
}

// A TURN IS HELD TO THE CHECKPOINT ITS TOOL LIST STATES. The loop is the
// one holder of a turn's numbers: it hands the definer the pair the turn
// begins with, and holds the turn to that pair to its end. A change made
// while a turn runs (a reload) reaches the next turn, list and rule
// together, and never the rule alone: that turn's tool list, sent with
// every one of its rounds, still states the old one.
func TestATurnIsHeldToTheCheckpointItsToolListStates(t *testing.T) {
	script := func(toolRounds int) *captureLLM {
		s := make([]llm.Response, 0, toolRounds+2)
		for i := 0; i < toolRounds; i++ {
			s = append(s, resp("", toolCall("c", "bash", `{}`)))
		}
		return &captureLLM{script: append(s, textResp("done"), textResp("done"))}
	}
	first, second := script(10), script(3)
	defs := &fakeDefs{}
	tools := &resetTools{fakeTools: fakeTools{results: map[string]string{"bash": "ok"}}, to: Checkpoint{Calls: 2, GraceRounds: 1}}
	loop := New(first, tools, defs, nil, nil,
		Config{MaxIterations: 60, PredictedThisTurn: func() (int, int) { return 0, 0 }, Checkpoint: Checkpoint{Calls: 1000, GraceRounds: 4}})
	tools.loop = loop
	run := func() Result {
		res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// The floor drops to 2 at this turn's third call. It began at 1000 and
	// its tool list says so: ten calls, no checkpoint.
	if res := run(); res.ContinuedAtCap || checkpointCount(first) != 0 || len(first.seen) != 11 {
		t.Errorf("a turn that began at a floor of 1000 was checkpointed by one set while it ran: %d requests, %d asks, capped=%v",
			len(first.seen), checkpointCount(first), res.ContinuedAtCap)
	}
	// The next turn begins at 2 and 1: armed at call 2, one round, the end.
	loop.llm = second
	if res := run(); !res.ContinuedAtCap || len(second.seen) != 4 {
		t.Errorf("the next turn did not take the changed checkpoint: %d requests, capped=%v; want 4 and capped", len(second.seen), res.ContinuedAtCap)
	}
	want := []Checkpoint{{Calls: 1000, GraceRounds: 4, Slack: DefaultCheckpointSlack}, {Calls: 2, GraceRounds: 1, Slack: DefaultCheckpointSlack}}
	if len(defs.handed) != 2 || defs.handed[0] != want[0] || defs.handed[1] != want[1] {
		t.Errorf("each turn's tool list was handed %+v; want the pair that turn was held to, %+v", defs.handed, want)
	}
}

// With no pair named the definer is handed the defaults, never a zero to
// write into a sentence.
func TestTheToolListIsHandedTheDefaultsWhereNoneIsNamed(t *testing.T) {
	defs := &fakeDefs{}
	loop := New(&captureLLM{script: []llm.Response{textResp("done")}}, &fakeTools{}, defs, nil, nil, Config{})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if want := (Checkpoint{Calls: DefaultCheckpointCalls, GraceRounds: DefaultCheckpointGraceRounds, Slack: DefaultCheckpointSlack}); len(defs.handed) != 1 || defs.handed[0] != want {
		t.Errorf("the tool list was handed %+v; want %+v", defs.handed, want)
	}
}

// A declared estimate of 3 buys 6 calls (slack ×2). The cap arms at
// call 6, the checkpoint ask arrives once naming the machine keys its
// consumer parses (key-for-key, lesson of the starved fan-out), the
// grace window elapses, and the turn ends flagged for continuation.
func TestDeclaredBudgetCapsTheTurnAndFlagsContinuation(t *testing.T) {
	client, res := runCapTurn(t, 10, 60, func() int { return 3 })

	if !res.ContinuedAtCap {
		t.Fatal("a turn ended by its declared budget did not flag ContinuedAtCap")
	}
	// Arming at the end of round 6 (calls=6 ≥ 3×2), grace 4 → forced
	// final after round 10: ten tool requests and one no-tools final.
	if got := len(client.seen); got != 11 {
		t.Errorf("turn took %d requests, want 11 — the declared budget did not bound it", got)
	}
	if got := checkpointCount(client); got != 1 {
		t.Errorf("checkpoint ask appeared %d times, want exactly 1", got)
	}
	all := joined(client.seen[len(client.seen)-1])
	for _, key := range []string{"work update next_move=", "plan="} {
		if !strings.Contains(all, key) {
			t.Errorf("the checkpoint ask omits %q — the machine key its consumer parses", key)
		}
	}
	if !strings.Contains(res.Spoken, "declared budget") || !strings.Contains(res.Spoken, "continues automatically in a fresh turn") {
		t.Errorf("the ending does not say what happened and what happens next: %q", res.Spoken)
	}
	// The loop cannot know whether a fresh turn follows. A caller that
	// scheduled none takes the reply closed on that: the model's words,
	// then the one line, and the loop's promise nowhere.
	const why = "no work session is active"
	if got, want := res.NothingContinues(why), "done\n\n"+nothingContinuesLine(res.ToolCallsUsed, why); got != want {
		t.Errorf("the reply of a capped turn nothing continues = %q, want %q", got, want)
	}
}

func nothingContinuesLine(calls int, why string) string {
	return "[This turn ended at its budget after " + itoa(calls) + " tool calls. Nothing continues by itself: " + why + ". Ask again to continue.]"
}

// No declaration: the floor (32 calls) bounds the turn instead, and the
// ask says so — declaring steps= is what buys a bigger budget.
func TestUndeclaredTurnGetsTheFloor(t *testing.T) {
	client, res := runCapTurn(t, 36, 60, func() int { return 0 })

	if !res.ContinuedAtCap {
		t.Fatal("an undeclared deep turn did not flag ContinuedAtCap at the floor")
	}
	// Arming at the end of round 32, grace 4 → 36 tool requests + final.
	if got := len(client.seen); got != 37 {
		t.Errorf("turn took %d requests, want 37 — the floor did not bound it", got)
	}
	all := joined(client.seen[len(client.seen)-1])
	if !strings.Contains(all, "no declared budget") || !strings.Contains(all, "declare steps=") {
		t.Error("the floor's ask does not tell the resident that declaring buys the budget")
	}
}

// A loop without PredictedThisTurn (safe boot, a sub-agent run without
// continuation legs) runs uncapped to its hard limit or its natural end.
func TestNoCapWithoutTheResidentWiring(t *testing.T) {
	client, res := runCapTurn(t, 40, 60, nil)

	if res.ContinuedAtCap {
		t.Fatal("a loop with no prediction wiring flagged ContinuedAtCap")
	}
	if got := len(client.seen); got != 41 {
		t.Errorf("turn took %d requests, want 41 (natural end) — an unwired loop was capped", got)
	}
	// A turn no budget ended has no promise to take back.
	if got := res.NothingContinues("no work session is active"); got != res.Spoken {
		t.Errorf("a turn that ended naturally was given a budget's closing line: %q", got)
	}
	if got := checkpointCount(client); got != 0 {
		t.Errorf("checkpoint ask appeared %d times in an unwired loop, want 0", got)
	}
}

// The model finishes inside the grace window: the ask was sent, the
// work wrapped naturally, and nothing flags — a natural ending always
// outranks the cap.
func TestNaturalFinishInsideGraceDoesNotFlag(t *testing.T) {
	client, res := runCapTurn(t, 8, 60, func() int { return 3 })

	if res.ContinuedAtCap {
		t.Fatal("a turn that ended naturally inside the grace window flagged ContinuedAtCap")
	}
	if got := checkpointCount(client); got != 1 {
		t.Errorf("checkpoint ask appeared %d times, want 1 — it should have fired before the natural end", got)
	}
}

// When the hard cap is nearer than cap+grace, the soft cap stands down:
// the hard ending owns the boundary and keeps its ask-again shape.
func TestSoftCapYieldsToTheHardCap(t *testing.T) {
	client, res := runCapTurn(t, 8, 8, func() int { return 3 })

	if res.ContinuedAtCap {
		t.Fatal("a hard-capped turn flagged ContinuedAtCap")
	}
	if got := checkpointCount(client); got != 0 {
		t.Errorf("checkpoint ask appeared %d times beside the hard cap, want 0", got)
	}
	if !strings.Contains(res.Spoken, "reached its limit") {
		t.Errorf("the hard cap lost its ask-again ending: %q", res.Spoken)
	}
}

// A missing prediction means no declared budget to keep — safe boot, or
// a sub-agent run without continuation legs. It runs to its HARD cap
// deterministically. This is the ONE reading of a nil accessor: the app
// always wires it for the resident.
func TestAMissingPredictionRunsToTheHardCapDeterministically(t *testing.T) {
	client, res := runCapTurn(t, 10, 60, nil)

	if res.ContinuedAtCap {
		t.Fatal("no prediction accessor means no declared budget to cap against — the hard cap owns the ending")
	}
	if n := checkpointCount(client); n != 0 {
		t.Fatalf("no checkpoint without a prediction accessor, got %d", n)
	}
}

// The checkpoint names its three outcomes in the resident's own verbs
// (the Method's converge/iterate/discard, mapped: done with its scope,
// continue from the point, stop with why), in both variants.
func TestCheckpointNoteNamesTheThreeOutcomes(t *testing.T) {
	for _, note := range []string{checkpointNote(3, 6, 4), checkpointNote(0, 32, 4)} {
		for _, want := range []string{checkpointMark, "next_move=", "done with its scope", "continue from that point", "stop with why"} {
			if !strings.Contains(note, want) {
				t.Errorf("checkpoint note missing %q: %s", want, note)
			}
		}
	}
	if !strings.Contains(checkpointNote(0, 32, 4), "steps=") {
		t.Error("the undeclared variant must still ask for steps=")
	}
}

// These older cases declare their budget before the first emitted call.
func budgetAtStart(predicted func() int) func() (remaining, ordinal int) {
	if predicted == nil {
		return nil
	}
	return func() (int, int) { return predicted(), 0 }
}
