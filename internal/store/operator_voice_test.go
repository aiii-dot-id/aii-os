package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestASpokenWordIsTheOperatorsOnlyWhenItsSpeakerWasIdentifiedAsThem(t *testing.T) {
	s := testStore(t)
	say := func(content string) uint64 {
		t.Helper()
		seq, err := s.AddConversationTurnSeq("operator", content)
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	stands := func(want uint64, why string) {
		t.Helper()
		turn, err := s.LatestOperatorWord()
		if err != nil || turn == nil || turn.TurnSeq != want {
			t.Fatalf("%s: want turn %d to stand as the operator's word, got %+v, %v", why, want, turn, err)
		}
	}
	refused := func(turn uint64, why, because string) {
		t.Helper()
		got, err := s.LatestOperatorWord()
		var unidentified *UnidentifiedSpeechError
		if got != nil || !errors.As(err, &unidentified) || unidentified.TurnSeq != turn || !strings.Contains(err.Error(), because) {
			t.Fatalf("%s: want turn %d refused because %q, got %+v, %v", why, turn, because, got, err)
		}
	}
	heard := func(turn uint64, payload string) {
		t.Helper()
		if err := s.AnnotateTurn(turn, AnnotationSpeaker, "vs-1/7", payload); err != nil {
			t.Fatal(err)
		}
	}

	typed := say("Yes, record it.")
	stands(typed, "a typed word, with no voice named")

	const (
		atDesk  = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
		onPhone = "b3a1f7d2-52c4-4e0b-8d19-7e6f5a4b3c2d"
		aGuest  = "c9d8e7f6-1a2b-4c3d-9e8f-0a1b2c3d4e5f"
	)
	as := func(uuid, continuity, rest string) string {
		return `{"speaker_uuid":"` + uuid + `","continuity":"` + continuity + `","registry_revision":"4"` + rest + `}`
	}
	spoken := say(SpokenMarker + "yes go ahead")
	refused(spoken, "spoken, and no speaker is named as the operator", "no speaker is named as the operator")
	s.SetOperatorVoices([]string{atDesk, onPhone})
	refused(spoken, "spoken, and nobody has said who spoke", "no speech engine has said who spoke it")
	heard(spoken, `{"decision":"unknown","speaker":""}`)
	refused(spoken, "an unknown speaker", "was not identified")
	// THE ENROLLED NAME DOES NOT DECIDE IT. An engine that knows the
	// speaker by an enrolled id, and gave no UUID, has not said the one
	// thing the rule reads; nor does an enrolled id that happens to be
	// spelled like one of the operator's UUIDs.
	heard(spoken, `{"decision":"known","speaker":"Quinn","speaker_id":"quinn-desk"}`)
	refused(spoken, "known by an enrolled name, and no UUID", "was not identified")
	heard(spoken, `{"decision":"known","speaker":"Quinn","speaker_id":"`+atDesk+`"}`)
	refused(spoken, "an enrolled id spelled as the operator's UUID", "was not identified")
	heard(spoken, as(atDesk, "provisional", `,"decision":"known","speaker_id":"quinn-desk"`))
	refused(spoken, "the operator's UUID, not yet settled", "was not identified")
	heard(spoken, as(atDesk, "", ""))
	refused(spoken, "the operator's UUID, and no continuity said", "was not identified")
	// The utterance that established a UUID was compared with nothing:
	// a list that already holds that UUID does not make it a match.
	heard(spoken, as(atDesk, "new_profile", `,"decision":"uncertain"`))
	refused(spoken, "the operator's UUID, on the utterance that established it", "was not identified")
	heard(spoken, as(aGuest, "matched", `,"decision":"known","speaker":"a guest","speaker_id":"guest-1"`))
	refused(spoken, "another person, matched", "someone other than the operator")

	heard(spoken, as(onPhone, "matched", `,"decision":"uncertain","speaker_id":""`))
	refused(spoken, "the operator's UUID, an anonymous speaker's", "did not identify the speaker")
	heard(spoken, as(onPhone, "matched", `,"decision":"unknown"`))
	refused(spoken, "the operator's UUID, the speaker unknown", "did not identify the speaker")
	heard(spoken, as(onPhone, "matched", `,"decision":"uncertain","speaker_id":"quinn-phone"`))
	refused(spoken, "the operator's UUID, an uncertain match to their enrolled voice", "did not identify the speaker")
	heard(spoken, as(onPhone, "matched", `,"decision":"known","speaker_id":""`))
	refused(spoken, "the operator's UUID, known as nobody", "did not identify the speaker")
	heard(spoken, as(onPhone, "matched", `,"decision":"known","speaker":"Quinn","speaker_id":"quinn-phone"`))
	stands(spoken, "identified as an enrolled voice, matched to one of the operator's UUIDs")

	// A turn tied to a voice session's final is spoken, marker or none.
	tied := say("words with no marker")
	if err := s.AnnotateTurn(tied, AnnotationVoice, "vs-1/8", `{"session":"vs-1","sequence":8}`); err != nil {
		t.Fatal(err)
	}
	refused(tied, "tied to a voice session's final", "no speech engine has said who spoke it")

	// A typed word after it stands again, and the spoken one is not
	// reached past it.
	later := say("Typed: yes.")
	stands(later, "a typed word after spoken ones")

	// The operator names no voice any more: the identified turn no longer
	// stands either.
	again := say(SpokenMarker + "and once more")
	heard(again, as(atDesk, "matched", `,"decision":"known","speaker_id":"quinn-desk"`))
	stands(again, "identified while the voice is named")
	s.SetOperatorVoices(nil)
	refused(again, "identified, and the voice is no longer named", "no speaker is named as the operator")
}

// ADMISSION HOLDS THE SAME RULE, so a Ring 1 change that cites a spoken
// turn nobody was identified as saying fails closed whoever built it. It
// is the rule for the operator's affirmation alone: testimony that cites
// the same turn is as it was.
func TestPreflightRefusesASpokenAffirmationNobodyWasIdentifiedAsSaying(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB().Exec(`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at) VALUES
		('c2','s','operator','[voice] yes',2,'2026-09-01T00:00:00Z'),
		('c3','s','operator','[voice] yes indeed',3,'2026-09-01T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	const atDesk = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	s.SetOperatorVoices([]string{atDesk})
	if err := s.AnnotateTurn(3, AnnotationSpeaker, "vs-1/3", `{"decision":"known","speaker_id":"quinn-desk","speaker_uuid":"`+atDesk+`","continuity":"matched"}`); err != nil {
		t.Fatal(err)
	}
	operator := func(turn int, excerpt string) []byte {
		return mustRaw(t, map[string]any{"id": "rel_spoken_turn", "counterpart_name": "Op", "counterpart_role": "operator",
			"relationship_type": "founding_operator", "charter_text": "charter",
			"operator_approval_excerpt": excerpt, "operator_approval_turn": turn, "approval_basis": "conversation_turn"})
	}
	ring := legalRing(t, ledger.EventRelationshipUpsert)
	err := s.ValidateEvent(ledger.EventRelationshipUpsert, ring, operator(2, "[voice] yes"))
	if err == nil || !strings.Contains(err.Error(), "cites a spoken turn that cannot stand as the operator's own word") || !strings.Contains(err.Error(), "fails closed") {
		t.Fatalf("a spoken affirmation nobody was identified as saying must fail closed: %v", err)
	}
	if err := s.ValidateEvent(ledger.EventRelationshipUpsert, ring, operator(3, "[voice] yes indeed")); err != nil {
		t.Fatalf("a spoken affirmation identified as the operator's was refused: %v", err)
	}
	testimony := mustRaw(t, map[string]any{"id": "x_spoken", "content": "it was said so", "provenance": "operator", "source_turn": 2})
	if err := s.ValidateEvent(ledger.EventExperienceCreate, legalRing(t, ledger.EventExperienceCreate), testimony); err != nil {
		t.Fatalf("testimony citing the same turn is not this rule's: %v", err)
	}
}
