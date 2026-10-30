package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// saidMark is the mark the opening or the writer left on the row with this
// id: its key, or "" when it carries none.
func saidMark(t *testing.T, s *Store, id string) string {
	t.Helper()
	var key string
	err := s.DB().QueryRow(`SELECT COALESCE((SELECT m.key FROM turn_annotations m JOIN conversations c ON c.turn_seq = m.turn_seq
		WHERE c.id = ? AND m.kind = ?), '')`, id, AnnotationSaid).Scan(&key)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// markRows keeps beside each row, by its turn_seq, the mark the writer
// keeps beside a row it records under the operator's role (AnnotationSaid),
// for a test that writes its rows by hand.
func markRows(t testing.TB, s *Store, marks map[uint64]string) {
	t.Helper()
	for seq, said := range marks {
		if _, err := s.DB().Exec(`INSERT INTO turn_annotations (turn_seq, kind, key, payload, created_at) VALUES (?, ?, ?, '{}', '2026-09-01T00:00:00Z')`,
			seq, AnnotationSaid, said); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheOperatorsMarkLeavesTheRowsDetailsAsThePredecessorReadsThem(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	record := func(content string, d interaction.Details) string {
		t.Helper()
		ref, err := s.RecordConversation(ctx, "operator", content, "", d)
		if err != nil {
			t.Fatal(err)
		}
		return ref.ID
	}
	rows := map[string]string{} // recorded in this order: the spoken word first, so the typed one is the latest word
	rows[record(SpokenMarker+"and the mornings", interaction.Details{Said: interaction.SaidSpoken})] = interaction.SaidSpoken
	rows[record("yes, record the charter", interaction.Details{Said: interaction.SaidTyped})] = interaction.SaidTyped
	rows[record(AskMarker+"ask_1] no — the answer is no; decide without it", interaction.Details{Said: interaction.SaidAnswer})] = interaction.SaidAnswer
	rows[record("a line some host feature writes under the operator's role", interaction.Details{})] = saidAtNoDoor
	for id, want := range rows {
		var details string
		if err := s.DB().QueryRow(`SELECT details FROM conversations WHERE id = ?`, id).Scan(&details); err != nil {
			t.Fatal(err)
		}
		if details != "{}" {
			t.Errorf("row %s holds details %s; the mark is not written into them (0.1.23 refuses a member it does not know)", id, details)
		}
		if got := saidMark(t, s, id); got != want {
			t.Errorf("row %s is marked %q beside it, want %q", id, got, want)
		}
	}
	page, err := s.QueryInteractions(ctx, interaction.Query{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page.Rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != len(rows) || strings.Contains(string(raw), `"said"`) {
		t.Fatalf("the page carries the mark (or lost a row): %s", raw)
	}
	if turn, err := s.LatestOperatorWord(); err != nil || turn == nil || turn.Content != "yes, record the charter" {
		t.Fatalf("the latest operator word is %+v (%v); want the typed turn", turn, err)
	}
}

func TestOperatorRowsAPredecessorRecordedAfterARollbackAreMarkedOnReturn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.RecordConversation(ctx, "operator", "keep the charter short", "t1", interaction.Details{Said: interaction.SaidTyped}); err != nil {
		t.Fatal(err)
	}
	card, err := s.RecordConversation(ctx, "operator", AskMarker+"ask_1] yes, record it as written", "t2", interaction.Details{Said: interaction.SaidTyped})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// As 0.1.23 records them, after a rollback: details as it writes them,
	// no mark beside them.
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, kind, turn_id, details) VALUES
		('p1','default','operator','no, leave the charter as it is',(SELECT MAX(turn_seq) FROM conversations)+1,'2026-10-21T00:00:00Z','message','p1','{}'),
		('p2','default','operator','[ask ask_2] chose: Yes, I affirm',(SELECT MAX(turn_seq) FROM conversations)+2,'2026-10-21T00:00:01Z','message','p2','{}'),
		('p3','default','operator','👍',(SELECT MAX(turn_seq) FROM conversations)+3,'2026-10-21T00:00:02Z','message','p3','{"grade":{"session":"s","grade":"up"}}')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, want := range map[string]string{"p1": interaction.SaidTyped, "p2": interaction.SaidAnswer, "p3": saidAtNoDoor, card.ID: interaction.SaidTyped} {
		if got := saidMark(t, s, id); got != want {
			t.Errorf("row %s is marked %q, want %q", id, got, want)
		}
	}
	turn, err := s.LatestOperatorWord()
	if err != nil || turn == nil || turn.Content != "no, leave the charter as it is" {
		t.Fatalf("the latest operator word is %+v (%v); want what they typed under 0.1.23, past its press and its grade", turn, err)
	}
	dialogue, err := s.LatestDialogueTurn()
	if err != nil || dialogue != "p2" {
		t.Fatalf("the latest turn of the dialogue is %q (%v); a press under 0.1.23 is one (want p2)", dialogue, err)
	}
}
