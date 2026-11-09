package store

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// THE WORDS THAT OPEN A TURN CARRY THE PATH THAT OPENED IT, beside the row
// (AnnotationTurnSource), never in its details, once per turn; a turn the
// identity's alarm opened carries the source of the turn that set the
// alarm; a turn whose context named no path has no known source. Resident
// rows, tool rows and notices carry none: they do not open turns.
func TestTheWordsThatOpenATurnCarryThePathThatOpenedIt(t *testing.T) {
	s := testStore(t)
	arrival := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-arrival"), "arrival")
	if _, err := s.RecordRelatedConversation(arrival, string(interaction.Participant), "[messages] a contact wrote", "turn-arrival", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRelatedConversation(arrival, string(interaction.Resident), "noted", "turn-arrival", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	source, setIn, err := s.TurnSourceOf("turn-arrival")
	if err != nil || source != "arrival" || setIn != "" {
		t.Fatalf("the arrival's turn reads %q %q %v, want arrival", source, setIn, err)
	}
	alarm := interaction.WithAlarmSetIn(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-alarm"), "timer"), "operator")
	if _, err := s.RecordRelatedConversation(alarm, string(interaction.System), "[timer t1 fired] tea — your own alarm woke you.", "turn-alarm", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	source, setIn, err = s.TurnSourceOf("turn-alarm")
	if err != nil || source != "timer" || setIn != "operator" {
		t.Fatalf("the alarm's turn reads %q %q %v, want timer set in the operator's turn", source, setIn, err)
	}
	unnamed := interaction.WithTurn(context.Background(), "turn-unnamed")
	if _, err := s.RecordRelatedConversation(unnamed, string(interaction.Operator), "hello", "turn-unnamed", "", interaction.Details{Said: interaction.SaidTyped}); err != nil {
		t.Fatal(err)
	}
	source, setIn, err = s.TurnSourceOf("turn-unnamed")
	if err != nil || source != "" || setIn != "" {
		t.Fatalf("a turn that named no path reads %q %q %v, want none", source, setIn, err)
	}
	var marks int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM turn_annotations WHERE kind = ?`, AnnotationTurnSource).Scan(&marks); err != nil {
		t.Fatal(err)
	}
	if marks != 2 {
		t.Fatalf("%d marks for two opened turns: the identity's own row or an unnamed turn was marked", marks)
	}
	var inDetails int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE details LIKE '%arrival%' OR details LIKE '%set_in%'`).Scan(&inDetails); err != nil {
		t.Fatal(err)
	}
	if inDetails != 0 {
		t.Fatalf("the path reached a row's details (%d rows): 0.2.0 reads details strictly", inDetails)
	}
	// Nothing serves it.
	page, err := s.QueryInteractions(context.Background(), interaction.Query{Version: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Rows {
		if _, served := r.Annotations[AnnotationTurnSource]; served {
			t.Fatalf("the path that opened the turn is served on row %s", r.ID)
		}
	}
}
