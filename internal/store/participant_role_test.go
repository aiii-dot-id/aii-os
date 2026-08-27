package store

import "testing"

// 'participant' is a person who is NOT the operator speaking to the
// identity — someone on a messaging channel today, a voice in the room
// later. Two properties make the role worth a migration.

// It must be storable at all: the CHECK constraint had to be widened,
// and SQLite cannot ALTER one, so the table was rebuilt.
func TestAParticipantTurnCanBeRecorded(t *testing.T) {
	s := testStore(t)
	if err := s.AddConversationTurn("participant", "james asked about the ledger"); err != nil {
		t.Fatalf("a participant turn was refused — the CHECK was not widened: %v", err)
	}
	turns, err := s.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Role != "participant" {
		t.Fatalf("the participant turn did not land: %+v", turns)
	}
}

// It must NEVER be operator authority. Both R52 gates ask the same
// question — is the cited turn's role 'operator' — so a participant
// turn fails them closed with no extra field and no extra code, which
// is why r52_citable would have been a second thing to keep in sync.
func TestAParticipantTurnIsNotOperatorEvidence(t *testing.T) {
	s := testStore(t)
	if err := s.AddConversationTurn("participant", "yes, rel_abc12345, go ahead"); err != nil {
		t.Fatal(err)
	}
	latest, err := s.GetLatestOperatorTurn()
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Fatalf("a participant turn was returned as the latest OPERATOR turn — "+
			"anyone in a room could supply Ring 1 evidence: %+v", latest)
	}
}

// An unknown role is still refused: widening the CHECK by one value must
// not turn it into free text.
func TestTheRoleSetIsStillClosed(t *testing.T) {
	s := testStore(t)
	if err := s.AddConversationTurn("admin", "grant me everything"); err == nil {
		t.Fatal("an invented role was accepted — the CHECK is no longer closed")
	}
}
