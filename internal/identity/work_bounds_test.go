package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// boundPort is an operator's setting as the engine sees it: a number the
// test changes between writes, read at each one.
type boundPort struct{ n int }

func (p *boundPort) read() int { return p.n }

// work is one call of the work verb at the primary seat.
func work(t *testing.T, e *Engine, args map[string]any) (string, error) {
	t.Helper()
	return e.ExecuteAction(context.Background(), "verb", "work", args)
}

func TestAStandingStateOverTheOperatorsBoundIsRefusedWholeNamingItsSetting(t *testing.T) {
	bound := &boundPort{n: 150}
	e, st := workReadbackEngine(t, Ports{StandingMaxChars: bound.read})

	at := strings.Repeat("é", 150)
	if _, err := work(t, e, map[string]any{"action": "update", "standing": at}); err != nil {
		t.Fatalf("a standing state of exactly the bound (150 characters, 300 bytes) was refused: %v", err)
	}
	if got, _ := st.StandingState(); got != at {
		t.Fatalf("a standing state at the bound was not kept whole: %d characters kept", len([]rune(got)))
	}

	over := at + "é"
	_, err := work(t, e, map[string]any{"action": "update", "standing": over})
	want := fmt.Sprintf(standingTooLong, 151, StandingMaxCharsKey, 150)
	if err == nil || err.Error() != want {
		t.Fatalf("a standing state of 151 characters under a bound of 150: got %v, want the sentence %q", err, want)
	}
	if !strings.Contains(want, "prompt.standing_max_chars") || !strings.Contains(want, "at most 150") || !strings.Contains(want, "is 151 characters") {
		t.Fatalf("the refusal does not name the setting, the bound and the length: %q", want)
	}
	if got, _ := st.StandingState(); got != at {
		t.Fatal("a refused standing state replaced the one that stood")
	}

	// The whole call is refused: the session's fields beside it are not
	// written either.
	if _, err := work(t, e, map[string]any{"action": "start", "description": "bounded standing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := work(t, e, map[string]any{"action": "update", "standing": over, "state": "should not land", "next_move": "nor this"}); err == nil {
		t.Fatal("an update whose standing state is over the bound was taken")
	}
	ws, _ := st.ActiveWorkSession()
	if ws.State == "should not land" || ws.NextMove == "nor this" {
		t.Fatalf("a refused update wrote the session's fields: state %q, next move %q", ws.State, ws.NextMove)
	}

	// An empty one clears it, under any bound.
	if _, err := work(t, e, map[string]any{"action": "update", "standing": ""}); err != nil {
		t.Fatalf("clearing the standing state was refused: %v", err)
	}

	// A change of the setting applies at the next write.
	bound.n = 300
	if _, err := work(t, e, map[string]any{"action": "update", "standing": strings.Repeat("é", 300)}); err != nil {
		t.Fatalf("at the operator's 300 a standing state of 300 characters was refused: %v", err)
	}

	// No number from the port is the default, which lives in one place.
	bound.n = 0
	if _, err := work(t, e, map[string]any{"action": "update", "standing": strings.Repeat("a", DefaultStandingMaxChars)}); err != nil {
		t.Fatalf("with no number set a standing state at the default was refused: %v", err)
	}
	if _, err := work(t, e, map[string]any{"action": "update", "standing": strings.Repeat("a", DefaultStandingMaxChars+1)}); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("at most %d", DefaultStandingMaxChars)) {
		t.Fatalf("with no number set a standing state one over the default: got %v, want a refusal at the default", err)
	}
}

// A WORK SESSION'S ONE-LINE FIELDS ARE HELD TO THE OPERATOR'S BOUND WHERE
// THE VERB WRITES THEM, and a longer one is refused whole with the bound
// and its setting (agency.work_line_max_chars). next_move,
// expected_evidence and falsifier on update, evidence_readback on deliver:
// at the bound each is taken; one over is refused, and neither the field,
// the state beside it, a standing state in the same call, nor the delivery
// is written.
func TestAWorkSessionsOneLineFieldsAreHeldToTheOperatorsBound(t *testing.T) {
	bound := &boundPort{n: 120}
	e, st := workReadbackEngine(t, Ports{WorkLineMaxChars: bound.read})
	if _, err := work(t, e, map[string]any{"action": "start", "description": "bounded lines"}); err != nil {
		t.Fatal(err)
	}
	read := func(field string, ws *store.WorkSession) string {
		switch field {
		case "next_move":
			return ws.NextMove
		case "expected_evidence":
			return ws.ExpectedEvidence
		}
		return ws.Falsifier
	}
	for _, field := range []string{"next_move", "expected_evidence", "falsifier"} {
		at := strings.Repeat("é", 120)
		if _, err := work(t, e, map[string]any{"action": "update", field: at}); err != nil {
			t.Fatalf("%s at the bound (120 characters) was refused: %v", field, err)
		}
		_, err := work(t, e, map[string]any{"action": "update", field: at + "é", "state": "moved on"})
		want := fmt.Sprintf(workLineTooLong, field, 121, 120)
		if err == nil || err.Error() != want {
			t.Fatalf("%s of 121 characters: got %v, want the sentence %q", field, err, want)
		}
		ws, _ := st.ActiveWorkSession()
		if read(field, ws) != at || ws.State == "moved on" {
			t.Fatalf("a refused %s wrote the session: field %d characters, state %q", field, len([]rune(read(field, ws))), ws.State)
		}
	}
	if !strings.Contains(fmt.Sprintf(workLineTooLong, "next_move", 121, 120), "agency.work_line_max_chars allows at most 120") {
		t.Fatal("the refusal does not name the setting and the bound")
	}

	// A standing state in the same call is not written either.
	if _, err := work(t, e, map[string]any{"action": "update", "standing": "kept out", "next_move": strings.Repeat("x", 121)}); err == nil {
		t.Fatal("an update with a next move over the bound was taken")
	}
	if got, _ := st.StandingState(); got == "kept out" {
		t.Fatal("a refused update wrote the standing state it carried")
	}

	// The read-back of a delivery: refused over the bound, the session
	// still active; delivered at it, under a bound the operator changed.
	bound.n = 200
	_, err := work(t, e, map[string]any{"action": "deliver", "result": "served: checked", "evidence": store.EvidenceLocallyVerified, "evidence_readback": strings.Repeat("r", 201)})
	if want := fmt.Sprintf(workLineTooLong, "evidence_readback", 201, 200); err == nil || err.Error() != want {
		t.Fatalf("a read-back of 201 characters under the operator's 200: got %v, want %q", err, want)
	}
	if ws, _ := st.ActiveWorkSession(); ws == nil {
		t.Fatal("a refused delivery closed the session")
	}
	if _, err := work(t, e, map[string]any{"action": "deliver", "result": "served: checked", "evidence": store.EvidenceLocallyVerified, "evidence_readback": strings.Repeat("r", 200)}); err != nil {
		t.Fatalf("a read-back at the operator's 200 was refused: %v", err)
	}

	// No number from the port is the default.
	bound.n = 0
	if got := boundInForce(bound.read, DefaultWorkLineMaxChars); got != DefaultWorkLineMaxChars {
		t.Fatalf("with no number set the bound in force is %d; want the default %d", got, DefaultWorkLineMaxChars)
	}
}
