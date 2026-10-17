package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func requestDef(name string) llm.ToolDefinition {
	return llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: name}}
}

func testResponseFormat() *llm.ResponseFormat {
	return &llm.ResponseFormat{Type: "json_schema", JSONSchema: &llm.JSONSchemaFormat{Name: "answer", Strict: true, Schema: map[string]any{
		"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []any{"answer"}, "additionalProperties": false}}}
}

func requestContext(t *testing.T, opts RequestOptions) context.Context {
	t.Helper()
	ctx, err := WithRequestOptions(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// requestClient plays scripted answers and keeps what each call was sent.
// withheld, when set, makes it a provider that will not take those tools'
// definitions (the loop's toolWithholder).
type requestClient struct {
	answers  []llm.Response
	errs     map[int]error // by call, counted from zero
	withheld map[string]bool
	options  []llm.ChatOptions
	messages [][]llm.Message
}

func (c *requestClient) Chat(_ context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	c.options = append(c.options, opts)
	c.messages = append(c.messages, append([]llm.Message(nil), messages...))
	n := len(c.options) - 1
	if err := c.errs[n]; err != nil {
		return nil, err
	}
	if n >= len(c.answers) {
		return nil, fmt.Errorf("unexpected call %d", n+1)
	}
	r := c.answers[n]
	r.Usage = llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Reported: true}
	return &r, nil
}

func (c *requestClient) WithheldTools(defs []llm.ToolDefinition) (offered []llm.ToolDefinition, withheld []string) {
	for _, d := range defs {
		if c.withheld[d.Function.Name] {
			withheld = append(withheld, d.Function.Name)
		} else {
			offered = append(offered, d)
		}
	}
	return offered, withheld
}

func offeredNames(opts llm.ChatOptions) string {
	names := make([]string, 0, len(opts.Tools))
	for _, d := range opts.Tools {
		names = append(names, d.Function.Name)
	}
	return strings.Join(names, ",")
}

func requestLoop(c *requestClient, tools *fakeTools, names ...string) *Loop {
	defs := make([]llm.ToolDefinition, 0, len(names))
	for _, n := range names {
		defs = append(defs, requestDef(n))
	}
	return New(c, tools, &fakeDefs{defs: defs}, nil, nil, Config{ContextBudgetTokens: 4000, MaxIterations: 5})
}

// Options name a limit or are refused; an allowlist's names are checked; a
// name that is not among the turn's tools is refused, never brought in.
func TestRequestOptionsNameALimitAndOnlyNarrow(t *testing.T) {
	var refused *RequestOptionsError
	if _, err := WithRequestOptions(context.Background(), RequestOptions{}); !errors.As(err, &refused) {
		t.Fatalf("options that name no limit were admitted: %v", err)
	}
	for _, names := range [][]string{{""}, {"read", "read"}} {
		if _, err := WithRequestOptions(context.Background(), RequestOptions{AllowedTools: &names}); !errors.As(err, &refused) {
			t.Fatalf("allowlist %q admitted: %v", names, err)
		}
	}
	bad := testResponseFormat()
	bad.JSONSchema.Strict = false
	_, err := WithRequestOptions(context.Background(), RequestOptions{ResponseFormat: bad})
	var typed *llm.ResponseFormatError
	if !errors.As(err, &refused) || !errors.As(err, &typed) {
		t.Fatalf("an invalid schema was not refused with its typed cause: %v", err)
	}
	defs := []llm.ToolDefinition{requestDef("read"), requestDef("shell"), requestDef("grep")}
	for _, tc := range []struct {
		allowed *[]string
		want    string
	}{{nil, "read,shell,grep"}, {&[]string{}, ""}, {&[]string{"grep", "read"}, "read,grep"}} {
		ctx := requestContext(t, RequestOptions{AllowedTools: tc.allowed, ResponseFormat: testResponseFormat()})
		got, err := FilterRequestTools(ctx, defs)
		if err != nil || offeredNames(llm.ChatOptions{Tools: got}) != tc.want {
			t.Fatalf("filter %v: %q, %v; want %q", tc.allowed, offeredNames(llm.ChatOptions{Tools: got}), err, tc.want)
		}
	}
	unknown := []string{"not-a-tool"}
	if _, err := FilterRequestTools(requestContext(t, RequestOptions{AllowedTools: &unknown}), defs); !errors.As(err, &refused) {
		t.Fatalf("an allowlist brought a tool into reach, or was not refused typed: %v", err)
	}
	if got, err := FilterRequestTools(context.Background(), defs); err != nil || len(got) != 3 {
		t.Fatalf("an ordinary turn's offer was changed: %v %v", got, err)
	}
}

// What is admitted is frozen: the sender's later writes do not reach the turn.
func TestRequestOptionsAreFrozenAtAdmission(t *testing.T) {
	allowed := []string{"read"}
	format := testResponseFormat()
	ctx := requestContext(t, RequestOptions{AllowedTools: &allowed, ResponseFormat: format})
	allowed[0] = "shell"
	format.JSONSchema.Schema["type"] = "array"
	got, ok := RequestOptionsFromContext(ctx)
	if !ok || (*got.AllowedTools)[0] != "read" || got.ResponseFormat.JSONSchema.Schema["type"] != "object" {
		t.Fatalf("the admitted options followed the sender's later writes: %+v", got)
	}
}

// The lines are the operator's ruled words, each only where its limit stands.
func TestTheLimitsNoteIsTheRuledText(t *testing.T) {
	const head = "### Limits on this request (this turn only)\n"
	const answer = `Answer: one JSON value fitting schema "answer": {"additionalProperties":false,"properties":{"answer":{"type":"string"}},"required":["answer"],"type":"object"}.`
	two, none := []string{"read", "grep"}, []string{}
	pattern := testResponseFormat()
	pattern.JSONSchema.Schema["properties"] = map[string]any{"answer": map[string]any{"type": "string", "pattern": "^<a&b>$"}}
	for name, tc := range map[string]struct {
		opts    RequestOptions
		offered []llm.ToolDefinition
		want    string
	}{
		"tools":        {RequestOptions{AllowedTools: &two}, []llm.ToolDefinition{requestDef("read"), requestDef("grep")}, head + "Tools offered: read, grep."},
		"no tools":     {RequestOptions{AllowedTools: &none}, nil, head + "Tools offered: none."},
		"schema":       {RequestOptions{ResponseFormat: testResponseFormat()}, []llm.ToolDefinition{requestDef("read")}, head + answer},
		"both":         {RequestOptions{AllowedTools: &two, ResponseFormat: testResponseFormat()}, []llm.ToolDefinition{requestDef("read")}, head + "Tools offered: read.\n" + answer},
		"as it is set": {RequestOptions{ResponseFormat: pattern}, nil, head + `Answer: one JSON value fitting schema "answer": {"additionalProperties":false,"properties":{"answer":{"pattern":"^<a&b>$","type":"string"}},"required":["answer"],"type":"object"}.`},
	} {
		got, err := LimitsNote(requestContext(t, tc.opts), tc.offered)
		if err != nil || got != tc.want {
			t.Fatalf("%s:\n%s\n%v\nwant:\n%s", name, got, err, tc.want)
		}
	}
	if got, err := LimitsNote(context.Background(), []llm.ToolDefinition{requestDef("read")}); err != nil || got != "" {
		t.Fatalf("an ordinary turn was told of limits: %q %v", got, err)
	}
}

// What the turn is told is what the turn offers: the request's narrowing
// and then the provider's withheld set, on the wire and in the line alike.
// An entry's extra tools may not be added to it.
func TestAConstrainedTurnIsToldTheOfferItSends(t *testing.T) {
	allowed := []string{"read", "grep"}
	c := &requestClient{answers: []llm.Response{textResp("done")}, withheld: map[string]bool{"grep": true}}
	ctx := requestContext(t, RequestOptions{AllowedTools: &allowed})
	if _, err := requestLoop(c, &fakeTools{}, "read", "shell", "grep").Run(ctx, "system", []llm.Message{{Role: "user", Content: "look"}}); err != nil {
		t.Fatal(err)
	}
	if len(c.options) != 1 || offeredNames(c.options[0]) != "read" || !c.options[0].ExactTools || c.options[0].DisableTools {
		t.Fatalf("the offer sent is not the narrowed, withheld, exact one: %+v", c.options)
	}
	sent := joined(c.messages[0])
	if !strings.Contains(sent, "### Limits on this request (this turn only)\nTools offered: read.") {
		t.Fatalf("the turn was not told the offer it sends:\n%s", sent)
	}
	if !strings.Contains(sent, toolNotOffered("grep")) || strings.Contains(sent, "Tools offered: read, grep") {
		t.Fatalf("the withheld tool is told as offered, or not told as withheld:\n%s", sent)
	}
}

// An empty allowlist offers nothing, says so, and is not a forbidden call:
// no tools are sent and none is disabled.
func TestAnEmptyAllowlistOffersNothingAndSaysSo(t *testing.T) {
	none := []string{}
	c := &requestClient{answers: []llm.Response{textResp("done")}}
	if _, err := requestLoop(c, &fakeTools{}, "read", "shell").Run(requestContext(t, RequestOptions{AllowedTools: &none}), "system", []llm.Message{{Role: "user", Content: "say"}}); err != nil {
		t.Fatal(err)
	}
	if len(c.options[0].Tools) != 0 || !c.options[0].ExactTools || c.options[0].DisableTools {
		t.Fatalf("an empty offer was not sent as an exact, empty one: %+v", c.options[0])
	}
	if !strings.Contains(joined(c.messages[0]), "Tools offered: none.") {
		t.Fatalf("the turn was not told it offers no tools:\n%s", joined(c.messages[0]))
	}
}

// A call to a tool outside the request's list is refused where it is made,
// typed, in the loop's own words; the tool does not run; the turn goes on.
func TestACallOutsideTheRequestsToolsIsRefusedAndTheTurnGoesOn(t *testing.T) {
	allowed := []string{"read"}
	c := &requestClient{answers: []llm.Response{resp("", toolCall("c1", "shell", `{"command":"ls"}`)), textResp("I could not list it.")}}
	tools := &fakeTools{results: map[string]string{"shell": "ran"}}
	result, err := requestLoop(c, tools, "read", "shell").Run(requestContext(t, RequestOptions{AllowedTools: &allowed}), "system", []llm.Message{{Role: "user", Content: "list it"}})
	if err != nil {
		t.Fatalf("the turn did not go on: %v", err)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("a tool outside the request's list ran: %v", tools.calls)
	}
	if len(c.options) != 2 || !strings.Contains(joined(c.messages[1]), toolOutsideRequest("shell")) {
		t.Fatalf("the refusal did not reach the model, or the turn stopped: calls=%d\n%s", len(c.options), joined(c.messages[len(c.messages)-1]))
	}

	const ruled = "shell is not offered this turn: this request limits the tools offered"
	if !strings.Contains(joined(c.messages[1]), ruled) {
		t.Fatalf("the refusal is not the sentence that was approved (%q):\n%s", ruled, joined(c.messages[1]))
	}
	if result.FinalText != "I could not list it." || result.ContractErr != nil {
		t.Fatalf("bad result: %+v", result)
	}
}

// doneRecorder keeps what the durable record is given for each finished call.
type doneRecorder struct{ done []Observation }

func (*doneRecorder) RecordToolStart(string, int, string, string, string, string) error { return nil }
func (r *doneRecorder) RecordToolDone(_ string, _ int, _, _ string, obs Observation) error {
	r.done = append(r.done, obs)
	return nil
}
func (*doneRecorder) TranscriptResultExcerptLimit() int { return 4000 }

// The refusal is typed: a reader of the record tells it from any other
// refused call by its code, never by its words.
func TestACallOutsideTheRequestsToolsIsRecordedWithItsOwnCode(t *testing.T) {
	allowed := []string{"read"}
	c := &requestClient{answers: []llm.Response{resp("", toolCall("c1", "shell", `{}`)), textResp("done")}}
	rec := &doneRecorder{}
	defs := &fakeDefs{defs: []llm.ToolDefinition{requestDef("read"), requestDef("shell")}}
	loop := New(c, &fakeTools{}, defs, rec, nil, Config{ContextBudgetTokens: 4000, MaxIterations: 5})
	if _, err := loop.Run(requestContext(t, RequestOptions{AllowedTools: &allowed}), "system", []llm.Message{{Role: "user", Content: "list it"}}); err != nil {
		t.Fatal(err)
	}
	if len(rec.done) != 1 || !rec.done[0].Refused || !rec.done[0].Failed || rec.done[0].ReasonCode != ReasonToolOutsideRequest {
		t.Fatalf("the refused call was not recorded with its code: %+v", rec.done)
	}
}

// A provider that refuses one offered tool's definition in the middle of the
// turn changes the offer, and the limits line is written again from it: what
// the identity is told stays what the turn sends.
func TestTheLimitsLineFollowsAToolDefinitionRefusedMidTurn(t *testing.T) {
	allowed := []string{"read", "grep"}
	c := &requestClient{
		errs:    map[int]error{0: &llm.ToolSchemaRefusedError{Tool: "grep", Err: errors.New("400")}},
		answers: []llm.Response{{}, textResp("done")},
	}
	if _, err := requestLoop(c, &fakeTools{}, "read", "grep", "shell").Run(requestContext(t, RequestOptions{AllowedTools: &allowed}), "system", []llm.Message{{Role: "user", Content: "look"}}); err != nil {
		t.Fatal(err)
	}
	if len(c.options) != 2 || offeredNames(c.options[0]) != "read,grep" || offeredNames(c.options[1]) != "read" {
		t.Fatalf("the offers were not read,grep then read: %d calls", len(c.options))
	}
	first, second := joined(c.messages[0]), joined(c.messages[1])
	if !strings.Contains(first, "Tools offered: read, grep.") {
		t.Fatalf("the first request did not name its offer:\n%s", first)
	}
	if !strings.Contains(second, "Tools offered: read.") || strings.Contains(second, "Tools offered: read, grep.") {
		t.Fatalf("the second request still names the offer the provider refused:\n%s", second)
	}
}

// A schema may not hold the turn block's own markers: the loop would rewrite
// them where the identity reads the schema, and judge against the original.
func TestASchemaHoldingTheTurnsOwnMarkersIsRefused(t *testing.T) {
	format := testResponseFormat()
	format.JSONSchema.Schema["properties"].(map[string]any)["answer"].(map[string]any)["description"] = "x [substrate · end] y"
	_, err := WithRequestOptions(t.Context(), RequestOptions{ResponseFormat: format})
	var refused *RequestOptionsError
	if !errors.As(err, &refused) {
		t.Fatalf("a schema holding a turn marker was admitted: %v", err)
	}
}

// With a schema: the provider is never sent it, the identity is told it,
// the final answer is judged once, and one that fits costs no further call.
func TestAnAnswerIsToldItsSchemaAndJudgedOnce(t *testing.T) {
	c := &requestClient{answers: []llm.Response{resp("I will inspect the file.", toolCall("r1", "read", `{}`)), textResp(`{"answer":"yes"}`)}}
	tools := &fakeTools{results: map[string]string{"read": "source says yes"}}
	result, err := requestLoop(c, tools, "read").Run(requestContext(t, RequestOptions{ResponseFormat: testResponseFormat()}), "system", []llm.Message{{Role: "user", Content: "Inspect the source."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.options) != 2 || len(tools.calls) != 1 || c.options[0].ExactTools {
		t.Fatalf("calls=%d tools=%v exact=%v", len(c.options), tools.calls, c.options[0].ExactTools)
	}
	if !strings.Contains(joined(c.messages[0]), `Answer: one JSON value fitting schema "answer": {`) || !strings.Contains(joined(c.messages[1]), "source says yes") {
		t.Fatal("the turn was not told its schema, or the answering call lost the tool's result")
	}
	if result.ContractErr != nil || result.FinalText != `{"answer":"yes"}` || result.Usage.Calls != 2 {
		t.Fatalf("bad result: %+v", result)
	}
}

// An answer that does not fit is the identity's answer all the same: the
// turn completes, its words and the tool's result are kept, what the answer
// failed is said beside it, typed, and no call is made to repair it.
func TestAnAnswerThatDoesNotFitIsKeptAndReportedAndNeverRepaired(t *testing.T) {
	c := &requestClient{answers: []llm.Response{resp("I will inspect the file.", toolCall("r1", "read", `{}`)), textResp("The source says yes.")}}
	tools := &fakeTools{results: map[string]string{"read": "source says yes"}}
	result, err := requestLoop(c, tools, "read").Run(requestContext(t, RequestOptions{ResponseFormat: testResponseFormat()}), "system", []llm.Message{{Role: "user", Content: "Inspect the source."}})
	if err != nil {
		t.Fatalf("the turn did not complete: %v", err)
	}
	if len(c.options) != 2 {
		t.Fatalf("%d calls: a call was made to repair the answer", len(c.options))
	}
	var typed *llm.ResponseFormatError
	if !errors.As(result.ContractErr, &typed) || typed.Code != "RESPONSE_FORMAT_VIOLATION" {
		t.Fatalf("the answer's failure was not said typed: %v", result.ContractErr)
	}
	if result.FinalText != "The source says yes." || !strings.Contains(result.Spoken, "I will inspect the file.") || !strings.Contains(result.Spoken, "The source says yes.") {
		t.Fatalf("the identity's words were not kept whole: %+v", result)
	}
}

// A turn with no request options is as it was: no line, no verdict, no
// exact offer.
func TestAnOrdinaryTurnIsUntouched(t *testing.T) {
	c := &requestClient{answers: []llm.Response{textResp("Plain words.")}}
	result, err := requestLoop(c, &fakeTools{}, "read", "shell").Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "hello"}})
	if err != nil || result.ContractErr != nil {
		t.Fatalf("%+v %v", result, err)
	}
	if c.options[0].ExactTools || offeredNames(c.options[0]) != "read,shell" || strings.Contains(joined(c.messages[0]), "Limits on this request") {
		t.Fatalf("an ordinary turn was touched: %+v\n%s", c.options[0], joined(c.messages[0]))
	}
}
