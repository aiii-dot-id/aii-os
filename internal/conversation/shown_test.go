package conversation

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// growingDefs is a definer whose set grows after each read, as the live
// offer does when the identity offers an operation mid-turn.
type growingDefs struct{ names []string }

func (g *growingDefs) ToolDefinitions(Checkpoint) []llm.ToolDefinition {
	var defs []llm.ToolDefinition
	for _, n := range g.names {
		defs = append(defs, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: n}})
	}
	g.names = append(g.names, "offered_later")
	return defs
}

// shownProbe answers each call with what ShownThisTurn told it: the first
// answer for each name and the latest.
type shownProbe struct{ seen, seenFirst map[string][2]bool }

func (p *shownProbe) Execute(ctx context.Context, call llm.ToolCall) Observation {
	shown, inTurn := ShownThisTurn(ctx, call.Function.Name)
	if p.seenFirst == nil {
		p.seenFirst = map[string][2]bool{}
	}
	if _, ok := p.seenFirst[call.Function.Name]; !ok {
		p.seenFirst[call.Function.Name] = [2]bool{shown, inTurn}
	}
	p.seen[call.Function.Name] = [2]bool{shown, inTurn}
	return Observation{Text: "ok"}
}

// EACH REQUEST'S DEFINITIONS ANSWER WHICH THEY WERE (T73). An executor
// asks the turn's context: a name among the definitions the request it
// answers sent is shown; a name the definer lists only after that request
// was built — offered mid-turn — is not, in that response; the loop reads
// the definitions again before the next request, which shows it. A
// context no turn made answers inTurn false.
func TestTheExecutorIsToldWhichDefinitionsItsRequestSent(t *testing.T) {
	probe := &shownProbe{seen: map[string][2]bool{}}
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("1", "shown_tool", "{}"), toolCall("2", "offered_later", "{}")),
		resp("", toolCall("3", "offered_later", "{}")),
		textResp("done"),
	}}
	defs := &growingDefs{names: []string{"shown_tool"}}
	loop := New(client, probe, defs, nil, nil, testBounds(Config{}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "one"}}); err != nil {
		t.Fatal(err)
	}
	if got := probe.seen["shown_tool"]; got != [2]bool{true, true} {
		t.Fatalf("a name the request sent: shown, inTurn = %v", got)
	}
	if got := probe.seenFirst["offered_later"]; got != [2]bool{false, true} {
		t.Fatalf("a name the request did not send must not be shown in its response: shown, inTurn = %v", got)
	}
	if got := probe.seen["offered_later"]; got != [2]bool{true, true} {
		t.Fatalf("the next request of the turn sent it, so it is shown: shown, inTurn = %v", got)
	}
	if shown, inTurn := ShownThisTurn(context.Background(), "shown_tool"); shown || inTurn {
		t.Fatalf("outside a turn there is no sent set: shown %v inTurn %v", shown, inTurn)
	}
}
