package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestAnOperatorRowIsTheirWordOnlyWhereADoorMarkedIt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	record := func(content, turn string, d interaction.Details) uint64 {
		t.Helper()
		ref, err := s.RecordConversation(ctx, "operator", content, turn, d)
		if err != nil {
			t.Fatal(err)
		}
		return ref.Sequence
	}
	typed := record("yes, record the charter", "t1", interaction.Details{Said: interaction.SaidTyped})
	record(AskMarker+"ask_1] no — the answer is no; decide without it", "t2", interaction.Details{Said: interaction.SaidAnswer})
	record("a line some new host feature writes under the operator's role", "t3", interaction.Details{})

	turn, err := s.LatestOperatorWord()
	if err != nil {
		t.Fatal(err)
	}
	if turn == nil || turn.TurnSeq != typed {
		t.Fatalf("the latest operator word is %+v; want the typed turn %d, past the ask card's answer and the unmarked row", turn, typed)
	}
	dialogue, err := s.LatestDialogueTurn()
	if err != nil {
		t.Fatal(err)
	}
	if dialogue != "t2" {
		t.Fatalf("the latest turn of the dialogue is %q; an ask card's answer is one, an unmarked host row is not (want t2)", dialogue)
	}
}

func TestRowsAnEarlierProgramRecordedAreMarkedByTheRuleTheyWereHeldTo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	// As a program before the marks recorded them: details carry no said.
	if _, err := s.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, kind, details) VALUES
		('e1','s','operator','[voice] remember the window seat',1,'2026-09-01T00:00:00Z','message','{}'),
		('e2','s','operator','please record it',2,'2026-09-01T00:00:01Z','message','{}'),
		('e3','s','operator','[ask ask_1] chose: Yes, I affirm',3,'2026-09-01T00:00:02Z','message','{}'),
		('e4','s','operator','[voice] (heard in the room, not addressed to you) lunch?',4,'2026-09-01T00:00:03Z','message','{"channel":"voice","reason":"room speech"}'),
		('e5','s','operator','👍',5,'2026-09-01T00:00:04Z','message','{"grade":{"session":"s","grade":"up"}}'),
		('e6','s','operator','the act was approved',6,'2026-09-01T00:00:05Z','message','{"channel":"plugin","actor":"operator"}'),
		('e7','s','resident','noted',7,'2026-09-01T00:00:06Z','message','{}')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	latestTyped := func(st *Store, where string) {
		t.Helper()
		turn, err := st.LatestOperatorWord()
		if err != nil || turn == nil || turn.TurnSeq != 2 {
			t.Fatalf("%s: the latest operator word is %+v (%v); want turn 2, the typed one, past the ask card's answer", where, turn, err)
		}
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	latestTyped(ro, "a read-only mount of the earlier rows")
	ro.Close()

	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"e1": interaction.SaidSpoken, "e2": interaction.SaidTyped, "e3": interaction.SaidAnswer,
		"e4": saidAtNoDoor, "e5": saidAtNoDoor, "e6": saidAtNoDoor, "e7": "",
	} {
		if got := saidMark(t, s, id); got != want {
			t.Errorf("row %s is marked %q, want %q", id, got, want)
		}
	}
	latestTyped(s, "after the opening marked them")

	if _, err := s.RecordConversation(context.Background(), "operator", "a line some new host feature writes", "", interaction.Details{}); err != nil {
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
	var id string
	if err := s.DB().QueryRow(`SELECT id FROM conversations WHERE content = 'a line some new host feature writes'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if got := saidMark(t, s, id); got != saidAtNoDoor {
		t.Fatalf("a row recorded with no door's mark after the marks is marked %q at the next opening, want %q", got, saidAtNoDoor)
	}
	latestTyped(s, "after a later opening")
}
