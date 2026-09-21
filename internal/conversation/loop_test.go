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

// .

// .
// .
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
	results map[string]string
	calls   []string
	models  []string
}

func (f *fakeTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	f.calls = append(f.calls, call.Function.Name)
	f.models = append(f.models, llm.ModelIDFromContext(ctx))
	return Observation{Text: f.results[call.Function.Name]}
}

// .
// .
type fakeTranscript struct {
	events []toolEvent
	limit  int
	err    error
}

type toolEvent struct {
	tool, args, result string
	failed, truncated  bool
	phase              string
	turnID             string
	ordinal            int
}

func (f *fakeTranscript) RecordToolStart(turnID string, ordinal int, callID, tool, args, model string) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, toolEvent{tool: tool, args: args, phase: "started", turnID: turnID, ordinal: ordinal})
	return nil
}

func (f *fakeTranscript) RecordToolDone(turnID string, ordinal int, tool, args, result string, failed, truncated bool) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, toolEvent{tool: tool, args: args, result: result, failed: failed, truncated: truncated, phase: "done", turnID: turnID, ordinal: ordinal})
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

// .

// .
// .
// .
func TestSpokenAccumulationAcrossToolCalls(t *testing.T) {
	// .
	// .
	// .
	// .
	// .
	var lt strings.Builder
	for i := 0; i < 32; i++ {
		lt.WriteString("Let me think about part ")
		lt.WriteRune(rune('a' + i%26))
		lt.WriteString(" of this carefully. ")
	}
	longThought := lt.String()
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

// .
// .
// .
func TestTruncationBannerHonesty(t *testing.T) {
	huge := strings.Repeat("x", 40_000)
	llmClient := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "bash", `{"command":"cat big"}`)),
		textResp("done"),
	}}
	tools := &fakeTools{results: map[string]string{"bash": huge}}
	tr := &fakeTranscript{limit: 4000}
	loop := New(llmClient, tools, &fakeDefs{}, tr, nil, Config{})

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "run"}}); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	if len(tr.events) != 2 || tr.events[0].phase != "started" || tr.events[1].phase != "done" || len(tr.events[1].result) != 40_000 {
		t.Fatalf("transcript must record started then done with the FULL result: %d events", len(tr.events))
	}

	// .
	// .
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

// .
// .
// .
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
	// .
	// .
	// .
	sent := llm.Message{Role: "system", Content: system.Content + systemAdditions()}
	essential, err := llm.EstimateInputTokens([]llm.Message{sent, current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	const margin = 100
	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: essential + margin})
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("old question ", 200)},
		{Role: "assistant", Content: strings.Repeat("old answer ", 200)},
		current,
	}

	if _, err := loop.RunSystem(context.Background(), system, history, 3); err != nil {
		t.Fatal(err)
	}
	request := client.requests[0]
	carried := 0
	for _, m := range request {
		if strings.Contains(m.Content, current.Content) {
			carried++
		}
	}
	if carried != 1 {
		t.Fatalf("current operator message must appear exactly once, got %d", carried)
	}
	if last := request[len(request)-1]; last.Role != current.Role || !strings.HasSuffix(last.Content, current.Content) {
		t.Fatalf("current operator message is not last: %+v", request)
	}
	if request[0].Content != sent.Content {
		t.Fatalf("the omission receipt was written into the system message: %q", request[0].Content)
	}
	// .
	// .
	// .
	// .
	if !messagesContain(request, "individually searchable") || !messagesContain(request, "source=conversation") {
		t.Fatalf("omission receipt must state the mechanism and name the working source: %+v", request)
	}
	if !messagesContain(request, "older conversation turns are not shown") || !messagesContain(request, "recall(query=") {
		t.Fatal("history omission must be visible and name recall")
	}
	got, err := llm.EstimateInputTokens(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got > essential+margin {
		t.Fatalf("request uses %d tokens, limit %d", got, essential+margin)
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

// .
// .
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
	// .
	// .
	if !strings.Contains(result.FinalText, "Summary after tools.") {
		t.Fatalf("forced final text must be FinalText, got %q", result.FinalText)
	}
	if len(tools.calls) != 3 {
		t.Fatalf("expected exactly 3 tool calls (the cap), got %d", len(tools.calls))
	}

	// .
	// .
	// .
	// .
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

	// .
	// .
	// .
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

// .
// .
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
	if len(tr.events) != 2 || tr.events[0].phase != "started" || tr.events[0].tool != "grep" ||
		tr.events[1].phase != "done" || tr.events[1].result != "match" {
		t.Fatalf("transcript must record started then done: %+v", tr.events)
	}
}

// .
// .
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

// .
// .
// .
func TestAnnouncedIntentNudge(t *testing.T) {
	s := &scriptLLM{script: []llm.Response{
		textResp("I found the config section. Now let me read the ledger schema."),
		resp("Reading it.", toolCall("1", "read", `{"file_path":"x"}`)),
		textResp("Done: the schema has 17 tables."),
	}}
	ft := &fakeTools{results: map[string]string{"read": "schema..."}}
	l := New(s, ft, &fakeDefs{}, &fakeTranscript{limit: 4000}, &fakeEmitter{}, Config{HeuristicNudges: true, MaxIterations: 10})
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
	// .
	s2 := &scriptLLM{script: []llm.Response{
		textResp("Now let me check A."),
		textResp("Now let me check B."),
	}}
	l2 := New(s2, &fakeTools{}, &fakeDefs{}, &fakeTranscript{limit: 4000}, &fakeEmitter{}, Config{HeuristicNudges: true, MaxIterations: 10})
	if _, err := l2.Run(context.Background(), "sys", []llm.Message{{Role: "user", Content: "continue"}}); err != nil {
		t.Fatal(err)
	}
	if s2.calls != 2 {
		t.Fatalf("one nudge only, got %d calls", s2.calls)
	}
}

// .
// .
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

// .
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

// .
// .
// .
// .
// .
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

// .
// .
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

// .
// .
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

// .
// .
// .
// .
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

// .
// .
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

// .
// .
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

// .
// .
// .
// .
func TestOldTurnsAreAbridgedBeforeDropped(t *testing.T) {
	system := llm.Message{Role: "system", Content: "identity"}
	sent := llm.Message{Role: "system", Content: system.Content + systemAdditions()}
	// .
	// .
	// .
	para := strings.TrimSpace(strings.Repeat("earlier conversation detail that the resident wrote down at the time ", 12))
	old := llm.Message{Role: "user",
		Content: strings.TrimSpace(strings.Repeat(para+"\n\n", 8))}
	current := llm.Message{Role: "user", Content: "current question"}

	// .
	// .
	// .
	// .
	shrunk := llm.Message{Role: "user", Content: prompt.SummarizeUnits(old.Content, historyRoute)}
	// .
	currentAbridged := llm.Message{Role: "user", Content: TurnBlock("", 0, 1, false) + current.Content}
	lower, err := llm.EstimateInputTokens([]llm.Message{sent, shrunk, currentAbridged}, nil)
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
	last := req[len(req)-1]
	if !strings.HasPrefix(last.Content, TurnOpen) || !strings.Contains(last.Content, "shown abridged") {
		t.Fatalf("abridgement was not declared in the current message's block: %q", last.Content)
	}
	if req[0].Content != sent.Content {
		t.Fatalf("the abridgement touched the system message: %q", req[0].Content)
	}
	if !strings.HasSuffix(last.Content, TurnClose+"\n\n"+current.Content) {
		t.Fatalf("current operator message is not last, whole, after the block: %+v", req)
	}
}

// .
// .
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

// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
	if !strings.Contains(last.Content, "2 rounds remain") {
		t.Fatalf("the model was not warned before the cap: %q", last.Content)
	}
	// .
	// .
	// .
	if last.Role != "tool" {
		t.Fatalf("the warning must ride the tool result, got role %q", last.Role)
	}
}

// .
// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
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
	// .
	// .
	if !strings.Contains(sys, "may not be possible here") {
		t.Fatalf("the prompt names the platform but does not warn that reach differs: %q", sys)
	}
	// .
	if !strings.Contains(sys, "not of who you are") {
		t.Fatalf("a change of reach must be framed as circumstance, not identity: %q", sys)
	}
}

// .
// .
// .
// .
// .
func TestModelIsWarnedBeforeContextPressure(t *testing.T) {
	system := llm.Message{Role: "system", Content: "identity"}
	current := llm.Message{Role: "user",
		Content: strings.TrimSpace(strings.Repeat("a long question the operator asked in some detail ", 90))}
	sent := llm.Message{Role: "system", Content: system.Content + systemAdditions()}
	used, err := llm.EstimateInputTokens([]llm.Message{sent, current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	budget := used + 120
	if used*100 < budget*contextTightPercent {
		t.Fatalf("fixture is not tight enough: used=%d budget=%d", used, budget)
	}

	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: budget})
	if _, err := loop.RunSystem(context.Background(), system, []llm.Message{current}, 0); err != nil {
		t.Fatal(err)
	}
	req := client.requests[0]
	newest := req[len(req)-1].Content
	if !strings.Contains(newest, "context for this turn is nearly full") {
		t.Fatalf("the model was not warned while it could still act: %q", newest)
	}
	if !strings.Contains(newest, "your tools will be withdrawn") {
		t.Fatalf("the warning must say what is about to happen: %q", newest)
	}
	// .
	// .
	if !strings.HasPrefix(newest, TurnOpen) || !strings.HasSuffix(newest, current.Content) {
		t.Fatalf("the warning is not in the current message's block ahead of the words: %q", newest)
	}
	if req[0].Content != sent.Content {
		t.Fatal("the warning was written into the system message")
	}
}

// .
// .
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

// .
// .
// .
// .
// .
// .
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
	// .
	if !strings.Contains(sys, "the only record that anything happened") {
		t.Fatalf("the clause does not tie action to the tool call: %q", sys)
	}
	// .
	// .
	// .
	if !strings.Contains(sys, "or do not know, is always") {
		t.Fatalf("the clause forbids fabrication without permitting the alternative: %q", sys)
	}
}

// .
// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
func TestAFailedCallStillCountsAgainstTheTurn(t *testing.T) {
	first := resp("working", toolCall("1", "read", "{}"))
	first.Usage = llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Reported: true}
	second := resp("still working", toolCall("2", "read", "{}"))
	second.Usage = llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220, Reported: true}

	// .
	client := &scriptLLM{script: []llm.Response{first, second}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil,
		Config{ContextBudgetTokens: 100000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil {
		t.Fatal("the third call was scripted to fail; the turn should report the error")
	}
	u := res.Usage
	if u.Calls != 3 {
		t.Fatalf("calls = %d, want 3 — the failed call is a call that happened: %+v", u.Calls, u)
	}
	if u.Silent != 1 {
		t.Fatalf("silent = %d, want 1 — a failed call reported no usage: %+v", u.Silent, u)
	}
	if u.Complete() {
		t.Fatalf("a turn whose last call failed is not a complete account: %+v", u)
	}
	if u.TotalTokens != 330 {
		t.Fatalf("the two known calls must still be reported as a floor: %+v", u)
	}
}

// .
// .
// .
func TestOneSilentCallMakesTheTurnALowerBound(t *testing.T) {
	first := resp("working", toolCall("1", "read", "{}"))
	first.Usage = llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Reported: true}
	last := textResp("done")

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

// .
// .
// .
// .
// .
// .
func TestTheChannelClauseScopesTheGroundingClause(t *testing.T) {
	// .
	sys := strings.Join(strings.Fields(systemAdditions()), " ")
	grounding := strings.Index(sys, "Report only what you actually did")
	channel := strings.Index(sys, "governs what you claim, not how you speak")
	if grounding < 0 || channel < 0 {
		t.Fatalf("grounding=%d channel=%d in:\n%s", grounding, channel, sys)
	}
	if channel < grounding {
		t.Fatal("the channel clause must follow the grounding clause it scopes")
	}
	// .
	if !strings.Contains(sys, "or do not know, is always") {
		t.Fatal("the channel clause displaced the grounding clause's escape hatch")
	}
}
