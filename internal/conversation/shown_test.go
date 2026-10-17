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

// shownProbe answers each call with what ShownThisTurn told it.
type shownProbe struct{ seen map[string][2]bool }

func (p *shownProbe) Execute(ctx context.Context, call llm.ToolCall) Observation {
	shown, inTurn := ShownThisTurn(ctx, call.Function.Name)
	p.seen[call.Function.Name] = [2]bool{shown, inTurn}
	return Observation{Text: "ok"}
}

// THE TURN THAT SENT THE DEFINITIONS ANSWERS WHICH THEY WERE (T73). An
// executor asks the turn's context: a name among the definitions this
// turn sent is shown; a name the definer would list only now — offered
// after the turn built its set — is not, on every round of that turn; a
// context no turn made answers inTurn false. The next turn shows the
// name it sends.
func TestTheExecutorIsToldWhichDefinitionsThisTurnSent(t *testing.T) {
	probe := &shownProbe{seen: map[string][2]bool{}}
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("1", "shown_tool", "{}")),
		resp("", toolCall("2", "offered_later", "{}")),
		textResp("done"),
		resp("", toolCall("3", "offered_later", "{}")),
		textResp("done"),
	}}
	loop := New(client, probe, &growingDefs{names: []string{"shown_tool"}}, nil, nil, Config{})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "one"}}); err != nil {
		t.Fatal(err)
	}
	if got := probe.seen["shown_tool"]; got != [2]bool{true, true} {
		t.Fatalf("a name this turn sent: shown, inTurn = %v", got)
	}
	if got := probe.seen["offered_later"]; got != [2]bool{false, true} {
		t.Fatalf("a name the turn did not send must not be shown on any of its rounds: shown, inTurn = %v", got)
	}
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "two"}}); err != nil {
		t.Fatal(err)
	}
	if got := probe.seen["offered_later"]; got != [2]bool{true, true} {
		t.Fatalf("the next turn sent it, so it is shown: shown, inTurn = %v", got)
	}
	if shown, inTurn := ShownThisTurn(context.Background(), "shown_tool"); shown || inTurn {
		t.Fatalf("outside a turn there is no sent set: shown %v inTurn %v", shown, inTurn)
	}
}
