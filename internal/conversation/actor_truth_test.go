package conversation

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// actor_truth_test.go — what the loop knew and did not tell the actor.
//
// One identity ran for hours on a fallback window a fraction of its
// model's, and the loop's own arithmetic explained every symptom it
// reported: this turn's fresh reads were folded before eight legs of its
// own status prose in history; a fold replaced a 240-byte read with a
// 225-byte notice; the notice forbade the re-run that would have
// recovered it; the number it was held to was never shown; and a turn
// cut by context died where a turn cut by count went on, under prose
// promising otherwise. Each rule below fails if its fix is removed.

// paragraphs builds structured prose SummarizeUnits can halve.
func paragraphs(word string, n, per int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.Repeat(word+" ", per))
	}
	return b.String()
}

func toolResult(id, content string) llm.Message { return llm.FormatToolResult(id, content) }

func estimate(t *testing.T, msgs []llm.Message) int {
	t.Helper()
	n, err := llm.EstimateInputTokens(msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The order of yielding is the order of recoverability: an older turn is
// in the record and searchable; this turn's result is neither, and its
// notice forbids the re-run. History goes first.
func TestOldestHistoryYieldsBeforeThisTurnsEvidence(t *testing.T) {
	read := strings.Repeat("evidence line\n", 100) // ~1.4 KB this turn just asked for
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: paragraphs("older", 8, 40)},
		{Role: "assistant", Content: paragraphs("answered", 8, 40)},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", `{"file_path":"/tmp/x"}`)}},
		toolResult("c1", read),
	}
	st := fitState{current: 3}
	budget := estimate(t, msgs) - 400
	if err := fitRequest(&msgs, &st, "stable", nil, budget, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || !strings.HasPrefix(last.Content, read[:200]) {
		t.Fatalf("this turn's read was folded while history remained to yield: %.120q", last.Content)
	}
	if st.abridged == 0 && st.omitted == 0 {
		t.Fatal("nothing yielded, yet the request was over budget")
	}
	if st.folded != 0 {
		t.Fatalf("folded=%d: a fresh result yielded before the searchable history", st.folded)
	}
}

// A fold that pays goes before one that does not, whatever their age: a
// small read is kept whole while a large result can yield instead. At
// the wall — nothing left that pays — the small one folds rather than
// the request failing. Every fold leaves a line in the log.
func TestAPayingFoldGoesFirstAndTheWallFoldsAnything(t *testing.T) {
	logged := logsink.CaptureForTest(t)

	small, big := strings.Repeat("x", 300), strings.Repeat("y", 3000)
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "shell", `{"command":"wc -c f"}`)}},
		toolResult("c1", small),
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c2", "shell", `{"command":"cat f"}`)}},
		toolResult("c2", big),
	}
	st := fitState{current: 1}
	if err := fitRequest(&msgs, &st, "stable", nil, estimate(t, msgs)-400, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if msgs[3].Content != small {
		t.Fatalf("the older, smaller result folded while a paying fold was available: %.80q", msgs[3].Content)
	}
	if !strings.HasPrefix(msgs[5].Content, foldNoticePrefix) || st.folded != 1 {
		t.Fatalf("the large result did not fold alone: %.80q folded=%d", msgs[5].Content, st.folded)
	}
	if !strings.Contains(logged.String(), "fold: result 2 of this turn (shell, 3000 runes) folded under context pressure") {
		t.Fatalf("the fold left no line in the log: %q", logged.String())
	}

	// The wall: only the small result remains, the request is over by a
	// little, and folding it is better than failing.
	logged.Reset()
	alone := []llm.Message{msgs[0], msgs[1], msgs[2], toolResult("c1", small)}
	st = fitState{current: 1}
	if err := fitRequest(&alone, &st, "stable", nil, estimate(t, alone)-20, nil); err != nil {
		t.Fatalf("at the wall the small result should fold rather than the request fail: %v", err)
	}
	if !strings.HasPrefix(alone[3].Content, foldNoticePrefix) || !strings.Contains(logged.String(), "last resort") {
		t.Fatalf("the wall did not fold as a last resort: %.80q / %q", alone[3].Content, logged.String())
	}
}

// The notice keeps the path. A read-only call is named and its re-run
// licensed; an act is named and its re-run forbidden.
func TestAFoldedReadNamesItsReRunAndAnActDoesNot(t *testing.T) {
	readOnly := func(c llm.ToolCall) bool { return c.Function.Name == "read" }
	body := strings.Repeat("z", 2000)
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", `{"file_path":"/tmp/sqldict/sqldict.go","offset":76,"limit":60}`)}},
		toolResult("c1", body),
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c2", "shell", `{"command":"go build ./..."}`)}},
		toolResult("c2", body),
	}
	st := fitState{current: 1, readOnly: readOnly}
	// Over by more than one result's worth: both must fold.
	if err := fitRequest(&msgs, &st, "stable", nil, estimate(t, msgs)-1000, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if st.folded != 2 {
		t.Fatalf("folded=%d, want both", st.folded)
	}
	read, act := msgs[3].Content, msgs[5].Content
	if !strings.Contains(read, "read-only call read(") || !strings.Contains(read, "may be repeated exactly") {
		t.Fatalf("the read's notice does not license its re-run: %q", read)
	}
	if !strings.Contains(read, "/tmp/sqldict/sqldict.go") {
		t.Fatalf("the read's notice lost the path: %q", read)
	}
	if !strings.Contains(act, "it was shell(") || !strings.Contains(act, "do not repeat the tool") || strings.Contains(act, "may be repeated") {
		t.Fatalf("the act's notice licenses a re-run or forgets the call: %q", act)
	}
	// A loop that cannot classify licenses nothing.
	msgs[3] = toolResult("c1", body)
	st = fitState{current: 1}
	_ = fitRequest(&msgs, &st, "stable", nil, estimate(t, msgs)-600, nil)
	if strings.Contains(msgs[3].Content, "may be repeated") {
		t.Fatalf("an unclassified call was licensed for a re-run: %q", msgs[3].Content)
	}
}

// Under pressure the newest tool result carries the number: how much of
// the budget is used, whether the budget is a guess, and what yielded.
// Once per message, never on a person's words, never when nothing is
// under pressure.
func TestThePressureLineRidesTheNewestToolResult(t *testing.T) {
	big := strings.Repeat("w", 3000)
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", `{}`)}},
		toolResult("c1", big),
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c2", "read", `{}`)}},
		toolResult("c2", "small answer"),
	}
	st := fitState{current: 1, fallback: true}
	budget := estimate(t, msgs) - 400
	if err := fitRequest(&msgs, &st, "stable", nil, budget, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	last := msgs[len(msgs)-1].Content
	for _, want := range []string{"[context: ~", "of " + itoa(budget) + " tokens used", "FALLBACK guess", "1 tool result(s) folded this turn", "spawned sub-agent starts with an empty history"} {
		if !strings.Contains(last, want) {
			t.Errorf("the pressure line omits %q: %q", want, last)
		}
	}
	// fitFinalRequest fits twice; the line appears once.
	if err := fitRequest(&msgs, &st, "stable", nil, budget, nil); err != nil {
		t.Fatalf("second fit: %v", err)
	}
	if n := strings.Count(msgs[len(msgs)-1].Content, "[context: "); n != 1 {
		t.Fatalf("the pressure line appeared %d times", n)
	}
	// Not a guess: the fallback clause is absent.
	msgs[5] = toolResult("c2", "small answer")
	st = fitState{current: 1}
	msgs[3] = toolResult("c1", big)
	if err := fitRequest(&msgs, &st, "stable", nil, budget, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if got := msgs[5].Content; !strings.Contains(got, "[context: ") || strings.Contains(got, "FALLBACK") {
		t.Fatalf("a derived window was called a guess, or the line vanished: %q", got)
	}
	// Nothing under pressure: no line.
	calm := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", `{}`)}},
		toolResult("c1", "fine"),
	}
	st = fitState{current: 1}
	if err := fitRequest(&calm, &st, "stable", nil, 100000, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if strings.Contains(calm[3].Content, "[context: ") {
		t.Fatalf("a calm request carried a pressure line: %q", calm[3].Content)
	}
	// The person's words are never annotated: pressure on the first
	// round, before any tool result, writes nothing.
	first := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: paragraphs("history", 6, 40)},
		{Role: "assistant", Content: paragraphs("reply", 6, 40)},
		{Role: "user", Content: "now"},
	}
	st = fitState{current: 3}
	if err := fitRequest(&first, &st, "stable", nil, estimate(t, first)-200, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if strings.Contains(first[len(first)-1].Content, "[context: ") {
		t.Fatalf("the operator's message was annotated: %q", first[len(first)-1].Content)
	}
}

// The fit prices each note it adds by the one message the note changes,
// and that price is exactly what estimating the whole request again
// gives: at the budget the request needs with both notes, both ride and
// the line names the estimate it was priced at; one token less, and the
// context line withdraws while the warning stays. A note priced by its
// own text is off by the per-message rounding and escaping, and fails
// here before it can pass a fit the client's admission then refuses.
func TestTheFitPricesItsNotesExactly(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", `{}`)}},
		toolResult("c1", strings.Repeat("a line of \"quoted\" text\n", 400)),
	}
	warned := slices.Clone(msgs)
	warned[3].Content += contextTightNote
	used := estimate(t, warned)
	budget, want := used, ""
	for range 4 { // the line names the budget it is priced against
		want = warned[3].Content + contextLine(used, budget, 0, &fitState{})
		noted := slices.Clone(warned)
		noted[3].Content = want
		budget = estimate(t, noted)
	}
	if !strings.HasSuffix(want, contextLine(used, budget, 0, &fitState{})) || estimate(t, msgs)*100 < (budget-1)*contextTightPercent {
		t.Fatal("setup: the budget both notes need did not settle, or the request is not tight")
	}
	fit := func(budget int) string {
		m := slices.Clone(msgs)
		st := fitState{current: 1}
		if err := fitRequest(&m, &st, "stable", nil, budget, nil); err != nil {
			t.Fatalf("fit at %d: %v", budget, err)
		}
		return m[3].Content
	}
	if got := fit(budget); got != want {
		t.Fatalf("at the budget both notes need (%d), both ride as priced; the newest result ends %q", budget, got[len(warned[3].Content):])
	}
	if got := fit(budget - 1); got != warned[3].Content {
		t.Fatalf("one token under it (%d), the line withdraws and the warning stays; the newest result ends %q", budget-1, got[len(msgs[3].Content):])
	}
}

// A turn cut by context is unfinished by budget, like one cut by count,
// and says so; the app continues it the same way.
func TestAContextFilledTurnContinuesLikeACappedOne(t *testing.T) {
	// A system prompt that leaves little room, results shorter than a
	// fold notice (nothing can yield), and a tool schema wide enough
	// that withdrawing the tools is what lets the final answer fit:
	// the wall this ending exists for.
	// The padding stands for the rest of a composed prompt, sized so the
	// request sits at this test's 1000-token budget. The closing text grew
	// by showNote, so the padding gives that much back (here and in
	// final_call_sites_test.go and turn_block_test.go).
	system := strings.Repeat("s", 670-len(showNote)) + systemAdditions() // composed with the contract, as the app sends it
	small := strings.Repeat("r", 150)
	defs := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
		Name: "bash", Description: strings.Repeat("d", 900), Parameters: map[string]any{"type": "object"}}}}}
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "bash", "{}")),
		textResp("what I have"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": small}}, defs, nil, nil, testBounds(Config{
		ContextBudgetTokens: 1000,
		MaxIterations:       limits.Of(6),
	}))
	res, err := loop.Run(context.Background(), system, []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ContinuedAtPressure {
		t.Fatal("a turn ended by context pressure did not flag ContinuedAtPressure")
	}
	if res.ContinuedAtCap || res.ExhaustedBudget || res.ExhaustedCallBudget {
		t.Fatalf("a pressure ending was reported as a count running out: %+v", res)
	}
	for _, want := range []string{"The context filled during this turn after", "continues automatically in a fresh turn"} {
		if !strings.Contains(res.Spoken, want) {
			t.Errorf("the ending omits %q: %q", want, res.Spoken)
		}
	}
	// When the caller schedules no fresh turn the reply closes on the one
	// line that says so, in the loop's line's place.
	const why = "no work session is active"
	want := "what I have\n\n" + nothingContinuesLine(res.ToolCallsUsed, why)
	if got := res.NothingContinues(why); got != want {
		t.Errorf("the reply of a context-filled turn nothing continues = %q, want %q", got, want)
	}
}

// The checkpoint promises only what is kept: a continuation follows the
// cap while the session is still active, and the prose says so.
func TestTheCheckpointPromiseIsConditional(t *testing.T) {
	for _, note := range []string{checkpointNote(3, 6, 4), checkpointNote(0, 32, 4)} {
		if !strings.Contains(note, "while its work session is still active") {
			t.Errorf("the checkpoint promises an unconditional continuation: %q", note)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
