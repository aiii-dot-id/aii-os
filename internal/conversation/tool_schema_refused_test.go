package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const unreadable = `^[^\p{Cc}]+$`

func toolDef(name, pattern string) llm.ToolDefinition {
	params := map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "pattern": pattern}}}
	return llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: name, Description: "the " + name + " tool", Parameters: params}}
}

// schemaProvider is a provider that refuses a whole request, as the real
// ones do, when one of its tools carries the unreadable pattern, naming
// that tool by its place in the list and in its dialect's own shape; and
// otherwise answers. It keeps what each request carried.
type schemaProvider struct {
	t        *testing.T
	dialect  string
	mu       sync.Mutex
	requests []providerRequest
	// call, when set, is a tool the first answered request calls; or,
	// with callWhen, every answered request for which it says so, by how
	// many were answered before it.
	call     string
	callWhen func(answered int) bool
	// refuse, when set, is the body every request is refused with.
	refuse string
	// refuseAt refuses the request of that ordinal (the first is 1) for
	// the definition of the tool at that place in its list, whatever the
	// definition says: a provider whose answer to the same list changed.
	refuseAt map[int]int
}

type providerRequest struct {
	tools []string
	body  string
	said  int // the HTTP status answered
}

func (p *schemaProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		p.t.Error(err)
		return
	}
	var req struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		p.t.Error(err)
		return
	}
	var names []string
	bad := -1
	for i, tool := range req.Tools {
		var shape struct {
			Name     string `json:"name"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		_ = json.Unmarshal(tool, &shape)
		names = append(names, shape.Name+shape.Function.Name)
		if bad < 0 && bytes.Contains(tool, []byte(`\\p{Cc}`)) {
			bad = i
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	answered := 0
	for _, r := range p.requests {
		if r.said == http.StatusOK {
			answered++
		}
	}
	if at, ok := p.refuseAt[len(p.requests)+1]; ok && bad < 0 {
		bad = at
	}
	calls := p.call != "" && answered == 0
	if p.call != "" && p.callWhen != nil {
		calls = p.callWhen(answered)
	}
	status, body := http.StatusOK, any(nil)
	switch {
	case p.refuse != "":
		status, body = http.StatusBadRequest, json.RawMessage(p.refuse)
	case bad >= 0 && p.dialect == "anthropic":
		status, body = http.StatusBadRequest, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error",
			"message": fmt.Sprintf("tools.%d.custom.input_schema: JSON schema is invalid. It must match JSON Schema draft 2020-12", bad)}}
	case bad >= 0:
		status, body = http.StatusBadRequest, map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": "invalid_function_parameters",
			"param": fmt.Sprintf("tools[%d].parameters", bad), "message": fmt.Sprintf("Invalid schema for function '%s': '^[^\\p{Cc}]+$' is not a 'regex'.", names[bad])}}
	case calls:
		body = map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "content": "",
			"tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": p.call, "arguments": "{}"}}}}}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "total_tokens": 11}}
	default:
		stop := "stop"
		if p.dialect == "anthropic" {
			stop = "end_turn"
		}
		body = pass4WireResponse(p.dialect, stop, "done", false, len(p.requests)+1)
	}
	p.requests = append(p.requests, providerRequest{tools: names, body: string(raw), said: status})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		p.t.Error(err)
	}
}

func (p *schemaProvider) seen() []providerRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]providerRequest(nil), p.requests...)
}

func schemaLoop(t *testing.T, p *schemaProvider, tools ToolExecutor, defs *fakeDefs, cfg Config) *Loop {
	t.Helper()
	p.t = t
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	client := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Provider: p.dialect, Model: "fixture", Retries: -1, TimeoutSeconds: 5, NoStream: true})
	if cfg.ContextBudgetTokens == 0 {
		cfg.ContextBudgetTokens = 100000
	}
	return New(client, tools, defs, nil, nil, cfg)
}

func line(name string) string {
	return name + " is not offered this turn: the model provider would not accept its definition"
}

func TestAToolWhoseDefinitionAProviderRefusesIsLeftOutAndTheTurnGoesOn(t *testing.T) {
	for _, dialect := range []string{"openai", "anthropic"} {
		t.Run(dialect, func(t *testing.T) {
			p := &schemaProvider{dialect: dialect}
			defs := &fakeDefs{defs: []llm.ToolDefinition{toolDef("read", "^[a-z]+$"), toolDef("correct", unreadable), toolDef("write", "^[a-z]+$")}}
			loop := schemaLoop(t, p, &fakeTools{}, defs, testBounds(Config{}))
			result, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
			if err != nil {
				t.Fatalf("THE TURN ENDED ON ONE TOOL'S DEFINITION: %v", err)
			}
			seen := p.seen()
			if len(seen) != 2 || fmt.Sprint(seen[0].tools) != "[read correct write]" || seen[0].said != http.StatusBadRequest ||
				fmt.Sprint(seen[1].tools) != "[read write]" || seen[1].said != http.StatusOK || !strings.Contains(result.Spoken, "done") {
				t.Fatalf("want the request refused once with all three tools, then answered with the tool the provider named left out and no other: %+v, spoken %q", seen, result.Spoken)
			}
			// The request that was refused was sent: the turn's meter counts
			// it, as a call that reported nothing.
			if result.Usage.Calls != 2 || result.Usage.Silent != 1 {
				t.Fatalf("two requests were sent, one of them refused, and the turn's meter says %d call(s), %d of them silent", result.Usage.Calls, result.Usage.Silent)
			}
			if strings.Contains(seen[0].body, line("correct")) || !strings.Contains(seen[1].body, line("correct")) {
				t.Fatalf("the identity is told the tool is not offered, in the request that leaves it out and not before:\n%s", seen[1].body)
			}
			if strings.Contains(seen[1].body, "is not a 'regex'") || strings.Contains(seen[1].body, "JSON schema is invalid") {
				t.Fatalf("the provider's own words were put to the identity:\n%s", seen[1].body)
			}

			// The next turn: the refused definition is not sent again.
			if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "again"}}); err != nil {
				t.Fatal(err)
			}
			seen = p.seen()
			if len(seen) != 3 || fmt.Sprint(seen[2].tools) != "[read write]" || !strings.Contains(seen[2].body, line("correct")) {
				t.Fatalf("THE NEXT TURN SENT THE REFUSED DEFINITION AGAIN, OR DID NOT SAY THE TOOL IS LEFT OUT: %d request(s), the last with %v\n%s", len(seen), seen[len(seen)-1].tools, seen[len(seen)-1].body)
			}

			// The plugin is fixed: its definition is another, and is offered.
			defs.defs = []llm.ToolDefinition{toolDef("read", "^[a-z]+$"), toolDef("correct", `^[^\x00-\x1f]+$`), toolDef("write", "^[a-z]+$")}
			if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "once more"}}); err != nil {
				t.Fatal(err)
			}
			seen = p.seen()
			if len(seen) != 4 || fmt.Sprint(seen[3].tools) != "[read correct write]" || strings.Contains(seen[3].body, line("correct")) {
				t.Fatalf("A CHANGED DEFINITION WAS NOT OFFERED AGAIN: %d request(s), the last with %v", len(seen), seen[len(seen)-1].tools)
			}
		})
	}
}

// ANY OTHER REFUSAL ENDS THE TURN AS IT DID. Only an answer that names one
// tool's definition in the field a provider writes for a program leaves a
// tool out; a 400 about anything else, or one whose sentence merely
// mentions a tool, is the request's own.
func TestARefusalThatNamesNoToolsDefinitionStillEndsTheTurn(t *testing.T) {
	for name, c := range map[string]struct{ dialect, body string }{
		"openai, another code":             {"openai", `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","param":"messages","message":"too long"}}`},
		"openai, a sentence about a tool":  {"openai", `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"messages[2]","message":"Invalid schema for function 'read'"}}`},
		"anthropic, another field":         {"anthropic", `{"type":"error","error":{"type":"invalid_request_error","message":"messages.0.content: text content blocks must be non-empty"}}`},
		"anthropic, a place past the list": {"anthropic", `{"type":"error","error":{"type":"invalid_request_error","message":"tools.7.custom.input_schema: JSON schema is invalid"}}`},
		"not JSON at all":                  {"openai", `tools[0] is bad`},
	} {
		t.Run(name, func(t *testing.T) {
			body := c.body
			if !json.Valid([]byte(body)) {
				raw, _ := json.Marshal(body)
				body = string(raw)
			}
			p := &schemaProvider{dialect: c.dialect, refuse: body}
			defs := &fakeDefs{defs: []llm.ToolDefinition{toolDef("read", "^[a-z]+$"), toolDef("write", "^[a-z]+$")}}
			loop := schemaLoop(t, p, &fakeTools{}, defs, testBounds(Config{}))
			_, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
			var refused *llm.ToolSchemaRefusedError
			var answered *llm.HTTPError
			if err == nil || errors.As(err, &refused) || !errors.As(err, &answered) || answered.StatusCode != http.StatusBadRequest || len(p.seen()) != 1 {
				t.Fatalf("want the turn ended by the provider's 400, after one request, with no tool left out: %v after %d request(s)", err, len(p.seen()))
			}
		})
	}
}

// A TURN LEAVES OUT AS MANY TOOLS AS IT MAY, AND NO MORE. Each refusal is a
// request that was not answered; past the bound the turn fails on the
// refusal, which is still the provider's answer beneath.
func TestATurnLeavesOutAsManyToolsAsItMayAndThenFailsOnTheRefusal(t *testing.T) {
	four := func() *fakeDefs {
		return &fakeDefs{defs: []llm.ToolDefinition{toolDef("read", "^[a-z]+$"), toolDef("a", unreadable), toolDef("b", unreadable), toolDef("c", unreadable), toolDef("d", unreadable)}}
	}
	p := &schemaProvider{dialect: "openai"}
	loop := schemaLoop(t, p, &fakeTools{}, four(), testBounds(Config{}))
	_, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	var refused *llm.ToolSchemaRefusedError
	var answered *llm.HTTPError
	if !errors.As(err, &refused) || refused.Tool != "d" || !errors.As(err, &answered) || len(p.seen()) != limits.DefaultToolSchemaRefusals+1 {
		t.Fatalf("WITH THE DEFAULT OF %d THE FOURTH REFUSAL ENDS THE TURN, NAMING ITS TOOL: %v after %d request(s)", limits.DefaultToolSchemaRefusals, err, len(p.seen()))
	}
	// What the client learned in that turn it keeps: the next turn sends
	// none of the four, and is answered at once.
	if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "again"}}); err != nil {
		t.Fatalf("the turn after: %v", err)
	}
	if seen := p.seen(); len(seen) != 5 || fmt.Sprint(seen[4].tools) != "[read]" {
		t.Fatalf("the turn after sends none of the four refused definitions: %d request(s), the last with %v", len(seen), seen[len(seen)-1].tools)
	}

	// The bound is the operator's: with four allowed, one turn does it.
	p = &schemaProvider{dialect: "openai"}
	loop = schemaLoop(t, p, &fakeTools{}, four(), testBounds(Config{ToolSchemaRefusals: 4}))
	if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil || len(p.seen()) != 5 {
		t.Fatalf("with four allowed the turn leaves out all four and is answered: %v after %d request(s)", err, len(p.seen()))
	}
}

// shownAsker is an executor that asks, for each call, what every executor
// may ask: was this tool among the definitions the turn shows?
type shownAsker struct {
	mu    sync.Mutex
	asked map[string][2]bool
}

func (s *shownAsker) Execute(ctx context.Context, call llm.ToolCall) Observation {
	shown, inTurn := ShownThisTurn(ctx, call.Function.Name)
	s.mu.Lock()
	if s.asked == nil {
		s.asked = map[string][2]bool{}
	}
	s.asked[call.Function.Name] = [2]bool{shown, inTurn}
	s.mu.Unlock()
	if !shown {
		return Observation{Text: "not shown this turn", Failed: true, Refused: true}
	}
	return Observation{Text: "ok"}
}

func TestACallThatNamesAToolLeftOutIsRefusedByTheLoop(t *testing.T) {
	p := &schemaProvider{dialect: "openai", call: "correct"}
	// The model calls the tool on the first answered request of each turn:
	// the first turn answers two requests (the call, then its last words).
	p.callWhen = func(answered int) bool { return answered == 0 || answered == 2 }
	defs := &fakeDefs{defs: []llm.ToolDefinition{toolDef("read", "^[a-z]+$"), toolDef("correct", unreadable)}}
	asker := &shownAsker{}
	loop := schemaLoop(t, p, asker, defs, testBounds(Config{}))
	refusal := "Error: [" + line("correct") + "]" // the substrate's own statement, bracketed as its others are
	if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if _, asked := asker.asked["correct"]; asked {
		t.Fatal("A CALL TO THE TOOL LEFT OUT THIS TURN REACHED THE EXECUTOR")
	}
	seen := p.seen()
	if len(seen) != 3 || !strings.Contains(seen[2].body, refusal) {
		t.Fatalf("want the refusal, the call and its answer, the call refused in the identity's own line: %d request(s)\n%s", len(seen), seen[len(seen)-1].body)
	}
	// The next turn withholds the definition from its start, and a call
	// that names it meets the same refusal.
	if _, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "again"}}); err != nil {
		t.Fatal(err)
	}
	if _, asked := asker.asked["correct"]; asked {
		t.Fatal("A CALL TO A TOOL WITHHELD FROM THE TURN'S START REACHED THE EXECUTOR")
	}
	seen = p.seen()
	if len(seen) != 5 || fmt.Sprint(seen[3].tools) != "[read]" || !strings.Contains(seen[4].body, refusal) {
		t.Fatalf("want the next turn's request without the tool, its call refused: %d request(s), the fourth with %v\n%s", len(seen), seen[min(3, len(seen)-1)].tools, seen[len(seen)-1].body)
	}
}

// A REFUSAL IN THE MIDDLE OF A TURN IS SAID WHERE THE TURN IS. After a round
// of tools the newest message is a tool's result, not the operator's words:
// the line rides its tail, and the request that leaves the tool out
// carries it.
func TestARefusalAfterARoundOfToolsIsSaidAtTheTailOfTheNewestResult(t *testing.T) {
	p := &schemaProvider{dialect: "openai", call: "read", refuseAt: map[int]int{2: 1}}
	defs := &fakeDefs{defs: []llm.ToolDefinition{toolDef("read", "^[a-z]+$"), toolDef("correct", "^[a-z]+$")}}
	loop := schemaLoop(t, p, &fakeTools{results: map[string]string{"read": "what was read"}}, defs, testBounds(Config{}))
	result, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	seen := p.seen()
	if len(seen) != 3 || seen[1].said != http.StatusBadRequest || fmt.Sprint(seen[2].tools) != "[read]" || !strings.Contains(result.Spoken, "done") {
		t.Fatalf("want the call, the refusal of the second request, and the third without the tool: %+v", seen)
	}
	if strings.Contains(seen[1].body, line("correct")) || !strings.Contains(seen[2].body, "what was read\\n["+line("correct")+"]") {
		t.Fatalf("THE IDENTITY WAS NOT TOLD, AT THE TAIL OF THE RESULT IT HAD JUST BEEN GIVEN, THAT THE TOOL IS LEFT OUT:\n%s", seen[2].body)
	}
}

// AND ON THE TURN'S LAST CALL. The call that ends a turn at its round
// ceiling forbids a tool call and still sends the definitions; a definition
// refused there is left out and the call sent again, as on any other.
func TestADefinitionRefusedOnTheTurnsFinalCallIsLeftOutThereToo(t *testing.T) {
	p := &schemaProvider{dialect: "openai", call: "read", refuseAt: map[int]int{2: 1}}
	defs := &fakeDefs{defs: []llm.ToolDefinition{toolDef("read", "^[a-z]+$"), toolDef("correct", "^[a-z]+$")}}
	loop := schemaLoop(t, p, &fakeTools{results: map[string]string{"read": "what was read"}}, defs, testBounds(Config{MaxIterations: limits.Of(1)}))
	result, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	seen := p.seen()
	if err != nil || len(seen) != 3 || !strings.Contains(seen[1].body, `"tool_choice":"none"`) || seen[1].said != http.StatusBadRequest ||
		fmt.Sprint(seen[2].tools) != "[read]" || !strings.Contains(result.Spoken, "done") {
		t.Fatalf("THE TURN'S FINAL CALL ENDED ON ONE TOOL'S DEFINITION: %v; requests %+v", err, seen)
	}
}
