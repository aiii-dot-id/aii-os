package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
)

// --- fakes ---

// scriptLLM plays a scripted sequence of responses; it records every
// request it received so tests can assert on what the MODEL saw.
type scriptLLM struct {
	script   []llm.Response
	calls    int
	requests [][]llm.Message
	toolsReq [][]llm.ToolDefinition
	thinking []int
}

func (s *scriptLLM) Chat(ctx context.Context, msgs []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	tools, tb := opts.Tools, opts.ThinkingBudget
	cp := make([]llm.Message, len(msgs))
	copy(cp, msgs)
	s.requests = append(s.requests, cp)
	s.toolsReq = append(s.toolsReq, tools)
	s.thinking = append(s.thinking, tb)
	if s.calls >= len(s.script) {
		return nil, fmt.Errorf("script exhausted at call %d", s.calls)
	}
	r := s.script[s.calls]
	s.calls++
	return &r, nil
}

func TestSetModelLimitsAppliesToNextWholeTurn(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("first"), textResp("second")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{
		ContextBudgetTokens: 1000,
		ThinkingBudget:      11,
	})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "one"}}); err != nil {
		t.Fatal(err)
	}
	loop.SetModelLimits(2000, 77)
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "two"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := client.thinking, []int{11, 77}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("thinking budget did not travel with provider change: got %v want %v", got, want)
	}
}

type fakeTools struct {
	results map[string]string // by tool name
	calls   []string
	models  []string
}

func (f *fakeTools) Execute(ctx context.Context, call llm.ToolCall) string {
	f.calls = append(f.calls, call.Function.Name)
	f.models = append(f.models, llm.ModelIDFromContext(ctx))
	return f.results[call.Function.Name]
}

// fakeTranscript records tool events VERBATIM (what the durable record
// received) and exposes its excerpt limit the way the store does.
type fakeTranscript struct {
	events []toolEvent
	limit  int
	err    error
}

type toolEvent struct{ tool, args, result string }

func (f *fakeTranscript) RecordToolEvent(tool, args, result string) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, toolEvent{tool, args, result})
	return nil
}
func (f *fakeTranscript) TranscriptResultExcerptLimit() int { return f.limit }

type fakeEmitter struct{ emitted []string }

func (f *fakeEmitter) EmitToolEvent(kind, name, args string) {
	f.emitted = append(f.emitted, kind+":"+name)
}

type fakeDefs struct{ defs []llm.ToolDefinition }

func (f *fakeDefs) ToolDefinitions() []llm.ToolDefinition { return f.defs }

func resp(content string, calls ...llm.ToolCall) llm.Response {
	return llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: content, ToolCalls: calls}}}}
}

func textResp(content string) llm.Response {
	return llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: content}}}}
}

func toolCall(id, name, args string) llm.ToolCall {
	var tc llm.ToolCall
	tc.ID = id
	tc.Type = "function"
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

func TestToolAndFinalTextKeepTheirProducingModels(t *testing.T) {
	first := resp("", toolCall("c1", "note", `{}`))
	first.ModelID = "model-a"
	second := textResp("done")
	second.ModelID = "model-b"
	tools := &fakeTools{results: map[string]string{"note": "ok"}}
	loop := New(&scriptLLM{script: []llm.Response{first, second}}, tools, &fakeDefs{}, nil, nil, Config{})

	result, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.models) != 1 || tools.models[0] != "model-a" {
		t.Fatalf("tool provenance = %v, want model-a", tools.models)
	}
	if result.ModelID != "model-b" {
		t.Fatalf("final-text provenance = %q, want model-b", result.ModelID)
	}
}

// --- the tests: every honesty property the package claims ---

// Spoken accumulation: content emitted ALONGSIDE a tool call must reach
// the operator. The live bug: 1108 chars of thought arrived with the tool
// call; only the 32-char post-tool tail reached the human.
func TestSpokenAccumulationAcrossToolCalls(t *testing.T) {
	// Varied sentences on purpose (2026-08-26): the old fixture repeated
	// ONE sentence verbatim 32 times — which is exactly the repetition
	// collapse the degenerate-emission guard now catches. This test is
	// about ACCUMULATION of spoken content, not repetition; the length
	// stays, the pathology goes.
	var lt strings.Builder
	for i := 0; i < 32; i++ {
		lt.WriteString("Let me think about part ")
		lt.WriteRune(rune('a' + i%26))
		lt.WriteString(" of this carefully. ")
	}
	longThought := lt.String() // ~1200 chars
	llmClient := &scriptLLM{script: []llm.Response{
		resp(longThought, toolCall("c1", "ls", "{}")),
		textResp("Here is what I found."),
	}}
	tools := &fakeTools{results: map[string]string{"ls": "file_a\nfile_b"}}
	tr := &fakeTranscript{limit: 4000}
	loop := New(llmClient, tools, &fakeDefs{}, tr, nil, Config{})

	result, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "list files"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Spoken, longThought) {
		t.Fatal("content spoken alongside a tool call was dropped from the reply")
	}
	if !strings.Contains(result.Spoken, "Here is what I found.") {
		t.Fatal("final text missing from the reply")
	}
	if len(tools.calls) != 1 || tools.calls[0] != "ls" {
		t.Fatalf("expected one ls call, got %v", tools.calls)
	}
}

// Banner honesty: when the model's view is truncated, the transcript
// receives the FULL result, and the banner's retention number is the
// transcript's OWN limit (it states what the recorder actually keeps).
func TestTruncationBannerHonesty(t *testing.T) {
	huge := strings.Repeat("x", 40_000) // > default 32k MaxToolResultChars
	llmClient := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "bash", `{"command":"cat big"}`)),
		textResp("done"),
	}}
	tools := &fakeTools{results: map[string]string{"bash": huge}}
	tr := &fakeTranscript{limit: 4000} // the recorder's real excerpt limit
	loop := New(llmClient, tools, &fakeDefs{}, tr, nil, Config{})

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "run"}}); err != nil {
		t.Fatal(err)
	}

	// 1. The transcript got the full result, untruncated
	if len(tr.events) != 1 || len(tr.events[0].result) != 40_000 {
		t.Fatalf("transcript must receive the FULL result (got %d chars)", len(tr.events[0].result))
	}

	// 2. The model's second request contains the banner, and the banner's
	// number matches the transcript's own limit
	banner := ""
	for _, m := range llmClient.requests[1] {
		if strings.Contains(m.Content, "[output truncated") {
			banner = m.Content
		}
	}
	if banner == "" {
		t.Fatal("truncated result fed to model without the honesty banner")
	}
	if !strings.Contains(banner, "the first 4000 characters are retained in the operator transcript") {
		t.Fatalf("banner must state the transcript's real retention limit (4000), got: %s", banner)
	}
}

// Context pressure folds bulk tool results before the next request. Every
// dispatched request stays within the model window, and the final call at the
// iteration limit carries no tools.
func TestContextGuardForceStops(t *testing.T) {
	huge := strings.Repeat("x", 20_000)
	llmClient := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "bash", "{}")),
		resp("", toolCall("c2", "bash", "{}")),
		{ModelID: "forced-model", Choices: []llm.Choice{{Message: llm.Message{Content: "Enough. Here is my answer from what I have."}}}},
	}}
	tools := &fakeTools{results: map[string]string{"bash": huge}}
	loop := New(llmClient, tools, &fakeDefs{}, nil, nil, Config{
		ContextBudgetTokens: 1000,
		MaxIterations:       2,
	})

	result, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Spoken, "Enough. Here is my answer") {
		t.Fatalf("forced guard response must become the reply, got %q", result.Spoken)
	}
	if result.FinalText != "Enough. Here is my answer from what I have." || result.ModelID != "forced-model" {
		t.Fatalf("forced guard provenance = (%q, %q)", result.FinalText, result.ModelID)
	}
	if len(tools.calls) != 2 {
		t.Fatalf("guard should stop tool use at 2 calls, got %d", len(tools.calls))
	}
	for i, request := range llmClient.requests {
		got, err := llm.EstimateInputTokens(request, llmClient.toolsReq[i])
		if err != nil {
			t.Fatal(err)
		}
		if got > 1000 {
			t.Fatalf("request %d uses %d tokens, limit 1000", i, got)
		}
	}
	lastReq := llmClient.requests[len(llmClient.requests)-1]
	if !messagesContain(lastReq, "do not repeat the tool solely") || !messagesContain(lastReq, "no transcript excerpt is available") {
		t.Fatal("folded tool output must prevent repeated side effects and state the available evidence")
	}
	if messagesContain(lastReq, "rerun the tool") {
		t.Fatal("folded tool output must not direct the model to repeat an arbitrary tool")
	}
	if len(llmClient.toolsReq[len(llmClient.toolsReq)-1]) != 0 {
		t.Fatal("forced final call must offer no tools")
	}
}

func TestOversizeRequiredContextRefusesBeforeLLM(t *testing.T) {
	client := &scriptLLM{}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 20})

	_, err := loop.Run(context.Background(), strings.Repeat("required", 100), []llm.Message{{Role: "user", Content: "current"}})
	var limitErr *llm.ContextLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("got %v, want ContextLimitError", err)
	}
	if client.calls != 0 || len(client.requests) != 0 {
		t.Fatal("oversize required context reached the LLM")
	}
}

func TestHistoryPressureKeepsCurrentOnceAndDeclaresOmission(t *testing.T) {
	system := llm.Message{Role: "system", Content: "required identity"}
	current := llm.Message{Role: "user", Content: "current question"}
	// Estimate against the system message the LOOP builds, not the one
	// handed in: RunSystem appends turnContract, and that text is part of
	// the protected floor this budget is meant to sit exactly on.
	sent := llm.Message{Role: "system", Content: system.Content + systemAdditions()}
	essential, err := llm.EstimateInputTokens([]llm.Message{sent, current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: essential + 80})
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("old question ", 200)},
		{Role: "assistant", Content: strings.Repeat("old answer ", 200)},
		current,
	}

	if _, err := loop.RunSystem(context.Background(), system, history, 3); err != nil {
		t.Fatal(err)
	}
	request := client.requests[0]
	if countMessage(request, current.Role, current.Content) != 1 {
		t.Fatal("current operator message must appear exactly once")
	}
	if last := request[len(request)-1]; last.Role != current.Role || last.Content != current.Content {
		t.Fatalf("current operator message is not last: %+v", request)
	}
	if !messagesContain(request, "older conversation turns are not shown") || !messagesContain(request, "recall(query=") {
		t.Fatal("history omission must be visible and name recall")
	}
	got, err := llm.EstimateInputTokens(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got > essential+80 {
		t.Fatalf("request uses %d tokens, limit %d", got, essential+80)
	}
}

func TestHistoryDropsLeadingOrphanAndRequiresCurrent(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{})
	history := []llm.Message{
		{Role: "assistant", Content: "orphaned old answer"},
		{Role: "user", Content: "current question"},
	}
	if _, err := loop.RunSystem(context.Background(), llm.Message{Role: "system", Content: "identity"}, history, 0); err != nil {
		t.Fatal(err)
	}
	if messagesContain(client.requests[0], "orphaned old answer") {
		t.Fatal("history began with an assistant reply whose user turn was absent")
	}
	if !messagesContain(client.requests[0], "1 older conversation turn") {
		t.Fatal("discarded orphan was not declared")
	}
	if _, err := loop.Run(context.Background(), "identity", nil); err == nil {
		t.Fatal("turn without a current user message was accepted")
	}
}

func TestTranscriptFailureStopsTurn(t *testing.T) {
	want := errors.New("disk full")
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("c1", "bash", "{}"))}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "result"}}, &fakeDefs{}, &fakeTranscript{err: want}, nil, Config{})

	_, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want transcript error", err)
	}
	if client.calls != 1 {
		t.Fatalf("continued after transcript failure: %d LLM calls", client.calls)
	}
}

func TestEmptyProviderResponseIsError(t *testing.T) {
	for _, response := range []llm.Response{{}, resp("   ")} {
		client := &scriptLLM{script: []llm.Response{response}}
		loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{})
		if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err == nil {
			t.Fatal("empty provider response was reported as success")
		}
	}
}

func TestIntentNudgeYieldsWhenItCannotFit(t *testing.T) {
	system := llm.Message{Role: "system", Content: "identity"}
	current := llm.Message{Role: "user", Content: "question"}
	sent := llm.Message{Role: "system", Content: system.Content + systemAdditions()}
	budget, err := llm.EstimateInputTokens([]llm.Message{sent, current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptLLM{script: []llm.Response{textResp("Let me read the file")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: budget})
	result, err := loop.RunSystem(context.Background(), system, []llm.Message{current}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || result.Spoken != "Let me read the file" {
		t.Fatalf("optional nudge displaced a valid response: calls=%d spoken=%q", client.calls, result.Spoken)
	}
}

func messagesContain(messages []llm.Message, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}

func countMessage(messages []llm.Message, role, content string) int {
	count := 0
	for _, message := range messages {
		if message.Role == role && message.Content == content {
			count++
		}
	}
	return count
}

// Last-iteration forcing: hitting MaxIterations with tool calls still
// outstanding forces one final no-tools call.
func TestLastIterationForcing(t *testing.T) {
	toolCallResp := func(i int) llm.Response {
		return resp("", toolCall(fmt.Sprintf("c%d", i), "bash", "{}"))
	}
	llmClient := &scriptLLM{script: []llm.Response{
		toolCallResp(1), toolCallResp(2), toolCallResp(3),
		textResp("Summary after tools."),
	}}
	tools := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop := New(llmClient, tools, &fakeDefs{}, nil, nil, Config{MaxIterations: 3})

	result, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	// The forced no-tools text lands in FinalText; with no earlier
	// spoken content the caller's reply IS FinalText.
	if !strings.Contains(result.FinalText, "Summary after tools.") {
		t.Fatalf("forced final text must be FinalText, got %q", result.FinalText)
	}
	if len(tools.calls) != 3 {
		t.Fatalf("expected exactly 3 tool calls (the cap), got %d", len(tools.calls))
	}

	// Regression (2026-08-17 review): when an earlier iteration spoke
	// alongside its tool call, the forced final summary was dropped from
	// Spoken — the operator only ever saw the earlier "Let me check…".
	// The forced text must reach the operator too.
	llmClient2 := &scriptLLM{script: []llm.Response{
		resp("Let me dig into that.", toolCall("c1", "bash", "{}")),
		toolCallResp(2), toolCallResp(3),
		textResp("Summary after tools."),
	}}
	tools2 := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop2 := New(llmClient2, tools2, &fakeDefs{}, nil, nil, Config{MaxIterations: 3})
	result2, err := loop2.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result2.Spoken, "Let me dig into that.") {
		t.Fatalf("earlier spoken content must survive, got %q", result2.Spoken)
	}
	if !strings.Contains(result2.Spoken, "Summary after tools.") {
		t.Fatalf("forced final summary must reach the operator reply, got %q", result2.Spoken)
	}

	// Prose beside the FINAL tool call is not a final answer: the model
	// has not seen that call's result yet. It still receives one bounded
	// no-tools call.
	llmClient3 := &scriptLLM{script: []llm.Response{
		resp("I am checking.", toolCall("c1", "bash", "{}")),
		textResp("Checked: done."),
	}}
	loop3 := New(llmClient3, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil, Config{MaxIterations: 1})
	result3, err := loop3.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if llmClient3.calls != 2 || !strings.Contains(result3.Spoken, "Checked: done.") {
		t.Fatalf("final tool result was not returned to the model: calls=%d spoken=%q", llmClient3.calls, result3.Spoken)
	}
}

// Emitter + transcript: streaming happens at call time; the durable
// record follows execution with the result.
func TestEmitAndRecordOrder(t *testing.T) {
	em := &fakeEmitter{}
	tr := &fakeTranscript{limit: 4000}
	llmClient := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "grep", `{"pattern":"x"}`)),
		textResp("done"),
	}}
	tools := &fakeTools{results: map[string]string{"grep": "match"}}
	loop := New(llmClient, tools, &fakeDefs{}, tr, em, Config{})

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if len(em.emitted) != 1 || em.emitted[0] != "tool_call:grep" {
		t.Fatalf("emitter must stream the call: %v", em.emitted)
	}
	if len(tr.events) != 1 || tr.events[0].tool != "grep" || tr.events[0].result != "match" {
		t.Fatalf("transcript must record call+result: %+v", tr.events)
	}
}

// FinalText is the verbatim last model text — the caller parses verbs
// from it; Spoken is the operator reply. Both must survive separately.
func TestResultCarriesFinalTextSeparately(t *testing.T) {
	llmClient := &scriptLLM{script: []llm.Response{
		textResp("note(content=\"saw this\")\nI noticed something."),
	}}
	loop := New(llmClient, &fakeTools{}, &fakeDefs{}, nil, nil, Config{})

	result, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != result.Spoken {
		t.Fatalf("simple turn: FinalText should equal Spoken verbatim")
	}
	if !strings.Contains(result.FinalText, "note(content=") {
		t.Fatal("verb directives must survive verbatim in FinalText")
	}
}

// The narrate-then-stall pattern (Aeon's report, 2026-08-18): a reply
// that ENDS by announcing the next step gets ONE nudge back to work;
// a finished answer never pays the extra call.
func TestAnnouncedIntentNudge(t *testing.T) {
	s := &scriptLLM{script: []llm.Response{
		textResp("I found the config section. Now let me read the ledger schema."),
		resp("Reading it.", toolCall("1", "read", `{"file_path":"x"}`)),
		textResp("Done: the schema has 17 tables."),
	}}
	ft := &fakeTools{results: map[string]string{"read": "schema..."}}
	l := New(s, ft, &fakeDefs{}, &fakeTranscript{limit: 4000}, &fakeEmitter{}, Config{MaxIterations: 10})
	res, err := l.Run(context.Background(), "sys", []llm.Message{{Role: "user", Content: "continue"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.calls != 3 {
		t.Fatalf("want 3 LLM calls (narrate -> nudge -> tool -> done), got %d", s.calls)
	}
	if len(ft.calls) != 1 || ft.calls[0] != "read" {
		t.Fatalf("the announced step must actually run, got %v", ft.calls)
	}
	if !strings.Contains(res.Spoken, "17 tables") {
		t.Fatalf("final answer must reach the operator: %q", res.Spoken)
	}
	// The nudge is once-per-turn: a second narration ends the turn.
	s2 := &scriptLLM{script: []llm.Response{
		textResp("Now let me check A."),
		textResp("Now let me check B."),
	}}
	l2 := New(s2, &fakeTools{}, &fakeDefs{}, &fakeTranscript{limit: 4000}, &fakeEmitter{}, Config{MaxIterations: 10})
	if _, err := l2.Run(context.Background(), "sys", []llm.Message{{Role: "user", Content: "continue"}}); err != nil {
		t.Fatal(err)
	}
	if s2.calls != 2 {
		t.Fatalf("one nudge only, got %d calls", s2.calls)
	}
}

// The five closers the 2026-08-18 bug hunt reproduced as false
// positives must NEVER nudge; announced WORK must still nudge.
func TestNudgeMatcherClosersAndWork(t *testing.T) {
	for _, closer := range []string{
		"Here's the summary. Let me know if you need anything else.",
		"Let me know if you need anything else.",
		"I'll now leave you to it.",
		"That's everything. I'm going to miss you.",
		"Done. Time to celebrate!",
	} {
		if endsInAnnouncedIntent(closer) {
			t.Errorf("closer must not nudge: %q", closer)
		}
	}
	for _, work := range []string{
		"I found the config section. Now let me read the ledger schema.",
		"Let me check the registry next.",
		"I'll run the tests now.",
		"I'm going to examine the manifest.",
	} {
		if !endsInAnnouncedIntent(work) {
			t.Errorf("announced work must nudge: %q", work)
		}
	}
}

// Ordinary conversation must never pay the nudge: one call, done.
func TestPlainAnswerNoNudge(t *testing.T) {
	s := &scriptLLM{script: []llm.Response{
		textResp("All five platforms are verified. The Windows pass closed the last gap."),
	}}
	l := New(s, &fakeTools{}, &fakeDefs{}, &fakeTranscript{limit: 4000}, &fakeEmitter{}, Config{MaxIterations: 10})
	if _, err := l.Run(context.Background(), "sys", []llm.Message{{Role: "user", Content: "question"}}); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("plain answers are one call, got %d", s.calls)
	}
}

// A completion the output cap cut off has the SAME SHAPE on the wire as
// a finished one: text, no tool calls, no error. Birth refuses on it
// (ceremony.go); a turn declares it, because refusing would discard
// work the resident already did. Without the declaration the operator
// reads half a sentence as the whole answer.
func TestOutputCapTruncationIsDeclared(t *testing.T) {
	cut := textResp("Here is the first half of the ans")
	cut.Choices[0].FinishReason = "length"
	client := &scriptLLM{script: []llm.Response{cut}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 1000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Spoken, "cut off by the model's output limit") {
		t.Fatalf("truncation was not declared to the operator: %q", res.Spoken)
	}
	if !strings.Contains(res.Spoken, "Here is the first half") {
		t.Fatalf("the partial answer must survive beside the declaration: %q", res.Spoken)
	}
}

// The declaration is evidence, and evidence that fires on every turn is
// noise. A clean stop must never be labelled truncated.
func TestCleanStopIsNotDeclaredTruncated(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("a complete answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 1000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Spoken, "cut off") {
		t.Fatalf("a finished reply was labelled truncated: %q", res.Spoken)
	}
}

// Truncation can recur across a long tool sequence. The operator needs
// the fact once, not a tally that grows with the turn.
func TestTruncationDeclaredOncePerTurn(t *testing.T) {
	first := resp("part one", toolCall("1", "read", "{}"))
	first.Choices[0].FinishReason = "length"
	second := textResp("part two")
	second.Choices[0].FinishReason = "length"
	client := &scriptLLM{script: []llm.Response{first, second}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil,
		Config{ContextBudgetTokens: 1000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(res.Spoken, "cut off by the model's output limit"); got != 1 {
		t.Fatalf("declaration should appear exactly once per turn, got %d: %q", got, res.Spoken)
	}
}

// A provider refusal arrives HTTP 200 with text. Collapsed to "stop" it
// rendered as the resident's own words — the exact thing R49 exists to
// prevent, one layer in: content the identity must learn to discount,
// presented as theirs.
func TestProviderRefusalIsNamedNotSpoken(t *testing.T) {
	r := textResp("I can't help with that.")
	r.Choices[0].FinishReason = "refusal"
	client := &scriptLLM{script: []llm.Response{r}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 1000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Spoken, "declined this request") {
		t.Fatalf("refusal was not named: %q", res.Spoken)
	}
	if !strings.Contains(res.Spoken, "not the identity's choice") {
		t.Fatalf("refusal must be attributed to the substrate, not the resident: %q", res.Spoken)
	}
}

// A stop reason added to the API after this build shipped must not be
// mistaken for a finished answer.
func TestUnknownStopReasonIsNamed(t *testing.T) {
	r := textResp("partial")
	r.Choices[0].FinishReason = "pause_turn"
	client := &scriptLLM{script: []llm.Response{r}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 1000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Spoken, "pause_turn") {
		t.Fatalf("unknown stop reason must be named with its value: %q", res.Spoken)
	}
}

// Negative control: the ordinary end of a turn must carry no note at
// all. A warning that fires on every turn teaches the reader to skip it.
func TestOrdinaryStopCarriesNoNote(t *testing.T) {
	for _, reason := range []string{"stop", ""} {
		r := textResp("a complete answer")
		r.Choices[0].FinishReason = reason
		client := &scriptLLM{script: []llm.Response{r}}
		loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 1000})
		res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Spoken, "[") {
			t.Fatalf("finish=%q produced a note on an ordinary completion: %q", reason, res.Spoken)
		}
	}
}

// The Accordion has always shrunk a ring section before dropping it.
// Conversation history was the one lossy path that only ever dropped
// turns WHOLE and reported a count, so the operator got a number where
// the identity could have kept the shape of what was said.
func TestOldTurnsAreAbridgedBeforeDropped(t *testing.T) {
	system := llm.Message{Role: "system", Content: "identity"}
	sent := llm.Message{Role: "system", Content: system.Content + systemAdditions()}
	// Paragraphs substantial enough that halving genuinely pays for the
	// declarations it adds — the guard in fitRequest refuses to abridge
	// when it would not.
	para := strings.TrimSpace(strings.Repeat("earlier conversation detail that the resident wrote down at the time ", 12))
	old := llm.Message{Role: "user",
		Content: strings.TrimSpace(strings.Repeat(para+"\n\n", 8))}
	current := llm.Message{Role: "user", Content: "current question"}

	// Budget must sit BETWEEN the abridged form and the whole one, and
	// the abridgement note is itself part of the abridged form — the
	// declaration costs budget, which is exactly the kind of thing an
	// estimate taken before the note is written gets wrong.
	shrunk := llm.Message{Role: "user", Content: prompt.SummarizeUnits(old.Content, historyRoute)}
	sentAbridged := llm.Message{Role: "system", Content: sent.Content + historyAbridgedNote(1)}
	lower, err := llm.EstimateInputTokens([]llm.Message{sentAbridged, shrunk, current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := llm.EstimateInputTokens([]llm.Message{sent, old, current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lower >= upper {
		t.Fatalf("abridging did not shrink the request: lower=%d upper=%d", lower, upper)
	}
	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: lower})
	if _, err := loop.RunSystem(context.Background(), system, []llm.Message{old, current}, 0); err != nil {
		t.Fatal(err)
	}

	req := client.requests[0]
	var abridged bool
	for _, m := range req {
		if strings.Contains(m.Content, prompt.SummaryMarker) {
			abridged = true
		}
	}
	if !abridged {
		t.Fatalf("the old turn was dropped whole instead of abridged: %+v", req)
	}
	if !strings.Contains(req[0].Content, "shown abridged") {
		t.Fatalf("abridgement was not declared in the system note: %q", req[0].Content)
	}
	if last := req[len(req)-1]; last.Content != current.Content {
		t.Fatalf("current operator message is not last: %+v", req)
	}
}

// An abridgement that fires on a turn with room to spare is noise, and
// it would quietly shorten history nobody asked to shorten.
func TestNoAbridgementWithoutPressure(t *testing.T) {
	system := llm.Message{Role: "system", Content: "identity"}
	old := llm.Message{Role: "user", Content: "first.\n\nsecond.\n\nthird."}
	current := llm.Message{Role: "user", Content: "current question"}
	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 100000})
	if _, err := loop.RunSystem(context.Background(), system, []llm.Message{old, current}, 0); err != nil {
		t.Fatal(err)
	}
	for _, m := range client.requests[0] {
		if strings.Contains(m.Content, prompt.SummaryMarker) {
			t.Fatalf("history was abridged with no context pressure: %q", m.Content)
		}
	}
}

// The iteration cap used to arrive without warning: the model was
// working, and the turn ended. Declaring it to the operator afterwards
// does not help the model land the work — it has to know the budget is
// nearly spent while it can still act on that.
func TestModelIsWarnedBeforeTheToolCap(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("working", toolCall("1", "read", "{}")),
		resp("still working", toolCall("2", "read", "{}")),
		textResp("done"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil,
		Config{ContextBudgetTokens: 100000, MaxIterations: 3})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) < 2 {
		t.Fatalf("expected at least two requests, got %d", len(client.requests))
	}
	second := client.requests[1]
	last := second[len(second)-1]
	if !strings.Contains(last.Content, "2 tool calls remain") {
		t.Fatalf("the model was not warned before the cap: %q", last.Content)
	}
	// The warning must ride an existing message, never add one: a second
	// consecutive user message breaks the alternating-role rule the
	// Anthropic dialect depends on.
	if last.Role != "tool" {
		t.Fatalf("the warning must ride the tool result, got role %q", last.Role)
	}
}

// A budget warning on a turn with room to spare is noise, and noise in
// the tool stream is exactly what the resident cannot afford to ignore.
func TestNoBudgetWarningWithRoomToSpare(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("working", toolCall("1", "read", "{}")),
		textResp("done"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil,
		Config{ContextBudgetTokens: 100000, MaxIterations: 20})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	for _, req := range client.requests {
		for _, m := range req {
			if strings.Contains(m.Content, "remain in this turn") {
				t.Fatalf("warned with 19 iterations left: %q", m.Content)
			}
		}
	}
}

// Reasoning is shown only when the provider actually returned it —
// blocks arrive with EMPTY text unless thinking_display is
// "summarized", so an operator who did not ask sees nothing and the
// event stream cannot fill with blanks.
func TestReasoningReachesTheOperatorOnlyWhenReturned(t *testing.T) {
	withText := textResp("answer")
	withText.Choices[0].Message.Thinking = []llm.ThinkingBlock{
		{Text: "weighing the options", Signature: "sig"},
	}
	empty := textResp("answer")
	empty.Choices[0].Message.Thinking = []llm.ThinkingBlock{{Text: "", Signature: "sig"}}

	for _, tc := range []struct {
		name string
		resp llm.Response
		want bool
	}{
		{"summarized: the operator sees it", withText, true},
		{"omitted: nothing to show", empty, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emitter := &fakeEmitter{}
			client := &scriptLLM{script: []llm.Response{tc.resp}}
			loop := New(client, &fakeTools{}, &fakeDefs{}, nil, emitter, Config{ContextBudgetTokens: 100000})
			if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
				t.Fatal(err)
			}
			var saw bool
			for _, e := range emitter.emitted {
				if strings.HasPrefix(e, "thinking:") {
					saw = true
				}
			}
			if saw != tc.want {
				t.Fatalf("thinking emitted = %v, want %v (events %v)", saw, tc.want, emitter.emitted)
			}
		})
	}
}

// An identity's tool reach is decided by platform, and SELF_MODEL writes
// what the identity can do from what it HAS done. A desktop-born
// self-model carried to a phone therefore asserts capabilities that no
// longer exist — in the identity's own voice, with nothing to explain
// the change. The prompt has to carry the fact the tool list cannot.
func TestTheIdentityIsToldItsPlatform(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("ok")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 100000})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	sys := client.requests[0][0].Content
	if !strings.Contains(sys, "all the reach this platform gives you") {
		t.Fatalf("the prompt does not state the reach of this platform: %q", sys)
	}
	if !strings.Contains(sys, platformName()) {
		t.Fatalf("the prompt does not name %q: %q", platformName(), sys)
	}
	// The warning matters more than the name: a remembered capability is
	// not evidence of a present one.
	if !strings.Contains(sys, "may not be possible here") {
		t.Fatalf("the prompt names the platform but does not warn that reach differs: %q", sys)
	}
	// And it must not read as an identity change.
	if !strings.Contains(sys, "not of who you are") {
		t.Fatalf("a change of reach must be framed as circumstance, not identity: %q", sys)
	}
}

// The tool-call cap warns two calls ahead so a turn can land. Context
// pressure had no equivalent: the request stopped fitting, the tools
// were withdrawn, and the model was told to answer now — all in the
// same breath. The operator was told afterwards; the model was told too
// late to choose differently.
func TestModelIsWarnedBeforeContextPressure(t *testing.T) {
	system := llm.Message{Role: "system", Content: "identity"}
	current := llm.Message{Role: "user",
		Content: strings.TrimSpace(strings.Repeat("a long question the operator asked in some detail ", 90))}
	sent := llm.Message{Role: "system", Content: system.Content + systemAdditions()}
	used, err := llm.EstimateInputTokens([]llm.Message{sent, current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Fits, but only just: above the tight threshold with room for the
	// warning itself.
	budget := used + 120
	if used*100 < budget*contextTightPercent {
		t.Fatalf("fixture is not tight enough: used=%d budget=%d", used, budget)
	}

	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: budget})
	if _, err := loop.RunSystem(context.Background(), system, []llm.Message{current}, 0); err != nil {
		t.Fatal(err)
	}
	sys := client.requests[0][0].Content
	if !strings.Contains(sys, "context for this turn is nearly full") {
		t.Fatalf("the model was not warned while it could still act: %q", sys)
	}
	if !strings.Contains(sys, "your tools will be withdrawn") {
		t.Fatalf("the warning must say what is about to happen: %q", sys)
	}
}

// A warning on every turn is noise, and noise in the system prompt is
// paid for on every request.
func TestNoContextWarningWithRoomToSpare(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 100000})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.requests[0][0].Content, "nearly full") {
		t.Fatalf("warned with a 100k budget: %q", client.requests[0][0].Content)
	}
}

// A live resident produced 4,584 characters describing work it had not
// performed — edits, a test, tool runs, a passing suite, and a
// fabricated mutation proof — in a single pass with ZERO tool calls.
// The grounding clause is the only text that reaches that turn: it is
// present on every iteration, including the first, where the
// tool-budget and context-pressure notes can never fire.
func TestEveryTurnCarriesTheGroundingClause(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("ok")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 100000})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	sys := client.requests[0][0].Content
	if !strings.Contains(sys, "Report only what you") {
		t.Fatalf("no grounding clause on the first turn: %q", sys)
	}
	// The tool call is what makes an action real; the clause has to say so.
	if !strings.Contains(sys, "the only record that anything happened") {
		t.Fatalf("the clause does not tie action to the tool call: %q", sys)
	}
	// And the escape hatch does as much work as the prohibition: an
	// identity with no acceptable way to say "I did not do that" is
	// under pressure to produce something that sounds like work.
	if !strings.Contains(sys, "or do not know, is always") {
		t.Fatalf("the clause forbids fabrication without permitting the alternative: %q", sys)
	}
}

// Both cut-off notes must ask for an HONEST close, not a tidy one. They
// fire exactly when the model is most tempted to round its account up.
func TestCutOffNotesAskForAnHonestClose(t *testing.T) {
	if !strings.Contains(toolBudgetNote(2), "name what you did not get to") {
		t.Fatalf("the tool-budget note invites a tidy ending: %q", toolBudgetNote(2))
	}
	if !strings.Contains(toolBudgetNote(2), "ACTUALLY done") {
		t.Fatalf("the tool-budget note does not anchor on what was actually done: %q", toolBudgetNote(2))
	}
	if !strings.Contains(contextTightNote, "name what remains undone") {
		t.Fatalf("the context note invites a tidy ending: %q", contextTightNote)
	}
}

// A turn is the unit that costs money, and nothing measured it. Each
// provider call was admitted against the same per-request ceiling, so a
// thirty-round turn could spend thirty allocations and no per-call limit
// would notice. Only the loop can add them up.
func TestTurnUsageAccumulatesAcrossEveryCall(t *testing.T) {
	first := resp("working", toolCall("1", "read", "{}"))
	first.Usage = llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Reported: true}
	second := resp("more", toolCall("2", "read", "{}"))
	second.Usage = llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220, Reported: true}
	last := textResp("done")
	last.Usage = llm.Usage{PromptTokens: 300, CompletionTokens: 30, TotalTokens: 330, Reported: true}

	client := &scriptLLM{script: []llm.Response{first, second, last}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil,
		Config{ContextBudgetTokens: 100000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	u := res.Usage
	if u.Calls != 3 {
		t.Fatalf("calls = %d, want 3 — every provider call in the turn counts", u.Calls)
	}
	if u.TotalTokens != 660 || u.PromptTokens != 600 || u.CompletionTokens != 60 {
		t.Fatalf("turn total wrong: %+v", u)
	}
	if !u.Complete() {
		t.Fatalf("every call reported; the turn total should be complete: %+v", u)
	}
}

// One silent call makes the whole turn a LOWER BOUND. Reporting it as a
// total would understate the spend, and understating is the direction
// that never refuses and never warns.
func TestOneSilentCallMakesTheTurnALowerBound(t *testing.T) {
	first := resp("working", toolCall("1", "read", "{}"))
	first.Usage = llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Reported: true}
	last := textResp("done") // no usage at all — the silent-provider case

	client := &scriptLLM{script: []llm.Response{first, last}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil,
		Config{ContextBudgetTokens: 100000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	u := res.Usage
	if u.Calls != 2 || u.Silent != 1 {
		t.Fatalf("calls/silent = %d/%d, want 2/1: %+v", u.Calls, u.Silent, u)
	}
	if u.Complete() {
		t.Fatalf("a turn containing a silent call is not a complete account: %+v", u)
	}
	if u.TotalTokens != 110 {
		t.Fatalf("known spend must still be reported as a floor: %+v", u)
	}
}
