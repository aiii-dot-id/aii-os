package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// A COMMITMENT'S STATE CHANGE TAKES result, repair_state AND note AT THE
// TOOL, as the record declares them. The tool had offered outcome, the
// record refused it and asked for result, and the tool did not take
// result; the promise closed with no line saying how. The schema names the three; the act keeps what is given; outcome on a
// commitment is still the record's refusal, naming the members it takes.
func TestACommitmentsStateChangeTakesItsResultAtTheTool(t *testing.T) {
	props, _ := lookupVerb("commit").Params["properties"].(map[string]any)
	for name, act := range map[string]string{"result": "commitment.state_change to completed, abandoned or repaired", "repair_state": "commitment.state_change to abandoned or repaired", "note": "commitment.state_change"} {
		p, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("the commit tool does not take %s", name)
		}
		if desc, _ := p["description"].(string); !strings.HasPrefix(desc, "("+act+")") {
			t.Fatalf("%s is not described for its act: %q", name, desc)
		}
	}
	if desc, _ := props["outcome"].(map[string]any)["description"].(string); !strings.HasPrefix(desc, "(intention.state_change") {
		t.Fatalf("outcome is not described as an intention's: %q", desc)
	}

	engine, st, lg, kp, _ := setupEngine(t)
	ctx := interaction.WithTurn(context.Background(), "turn_close")
	recordPromise(t, engine, st, lg, kp, "cm_done")
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "commitment.state_change", "id": "cm_done", "state": "completed", "result": "served: the summary was sent on Friday", "note": "a day late",
	}); err != nil {
		t.Fatalf("a completed commitment with its result was refused: %v", err)
	}
	commitments, err := st.ListCommitments(false)
	if err != nil || len(commitments) != 1 || commitments[0].State != "completed" || commitments[0].Result != "served: the summary was sent on Friday" {
		t.Fatalf("the record does not carry the result: %+v %v", commitments, err)
	}
	_, err = engine.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "commitment.state_change", "id": "cm_done", "state": "completed", "outcome": "served: the summary was sent on Friday",
	})
	if err == nil || !strings.Contains(err.Error(), `commitment.state_change does not declare the member "outcome"`) || !strings.Contains(err.Error(), "result") {
		t.Fatalf("outcome on a commitment was not refused naming the members it takes: %v", err)
	}
}
