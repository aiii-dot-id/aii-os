package conversation

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
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

// .
// .
// .
func TestOldestHistoryYieldsBeforeThisTurnsEvidence(t *testing.T) {
	read := strings.Repeat("evidence line\n", 100)
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

// .
// .
// .
// .
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

	// .
	// .
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

// .
// .
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
	// .
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
	// .
	msgs[3] = toolResult("c1", body)
	st = fitState{current: 1}
	_ = fitRequest(&msgs, &st, "stable", nil, estimate(t, msgs)-600, nil)
	if strings.Contains(msgs[3].Content, "may be repeated") {
		t.Fatalf("an unclassified call was licensed for a re-run: %q", msgs[3].Content)
	}
}

// .
// .
// .
// .
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
	// .
	if err := fitRequest(&msgs, &st, "stable", nil, budget, nil); err != nil {
		t.Fatalf("second fit: %v", err)
	}
	if n := strings.Count(msgs[len(msgs)-1].Content, "[context: "); n != 1 {
		t.Fatalf("the pressure line appeared %d times", n)
	}
	// .
	msgs[5] = toolResult("c2", "small answer")
	st = fitState{current: 1}
	msgs[3] = toolResult("c1", big)
	if err := fitRequest(&msgs, &st, "stable", nil, budget, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if got := msgs[5].Content; !strings.Contains(got, "[context: ") || strings.Contains(got, "FALLBACK") {
		t.Fatalf("a derived window was called a guess, or the line vanished: %q", got)
	}
	// .
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
	// .
	// .
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

// .
// .
func TestAContextFilledTurnContinuesLikeACappedOne(t *testing.T) {
	// .
	// .
	// .
	// .
	system := strings.Repeat("s", 670)
	small := strings.Repeat("r", 150)
	defs := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
		Name: "bash", Description: strings.Repeat("d", 900), Parameters: map[string]interface{}{"type": "object"}}}}}
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "bash", "{}")),
		textResp("what I have"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": small}}, defs, nil, nil, Config{
		ContextBudgetTokens: 1000,
		MaxIterations:       6,
	})
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
}

// .
// .
func TestTheCheckpointPromiseIsConditional(t *testing.T) {
	for _, note := range []string{checkpointNote(3, 6, 4), checkpointNote(0, 32, 4)} {
		if !strings.Contains(note, "while its work session is still active") {
			t.Errorf("the checkpoint promises an unconditional continuation: %q", note)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
