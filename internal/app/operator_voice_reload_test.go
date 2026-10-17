package app

import (
	"testing"
)

// A VOICE TAKEN OUT OF THE FILE STOPS BEING THE OPERATOR'S AT THE RELOAD.
// speech.operator_speakers has two doors, the settings page and the file.
// The store that enforces the rule keeps its own copy of the list; the
// page's door handed it over and the file's did not, so a voice removed by
// editing config.json went on standing as the operator's word until the
// identity was restarted. It failed open.
func TestAVoiceRemovedInTheFileStopsStandingAtTheReload(t *testing.T) {
	const atDesk = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	dir := t.TempDir()
	key, ledgerPath, db := birthFixture(t, dir, "Quinn")
	a := newBareApp(t, safebootConfig(t, dir, "Quinn", key, ledgerPath, db))
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	a.voiceSessions.Store("vs-1", &voiceHandle{id: "vs-1"})

	seq, err := a.store.AddConversationTurnSeq(roleOperator, voiceMarker+"yes, record it")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.AnnotateTurn(seq, annotationVoice, voiceRefKey("vs-1", 7), `{"session":"vs-1"}`); err != nil {
		t.Fatal(err)
	}
	a.noteSpeakerObservation(decodeVoiceFrame(knownUUIDObservation("vs-1", 20, 7, "t1", "quinn-desk", atDesk, "matched")), false)
	stands := func() bool {
		t.Helper()
		turn, err := a.store.LatestOperatorWord()
		return err == nil && turn != nil && turn.TurnSeq == seq
	}

	// Named through the file: the reload must make the turn stand.
	named := a.configSnapshot()
	named.Speech.OperatorSpeakers = []string{atDesk}
	if _, err := saveConfig(&named); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Speech.OperatorSpeakers; len(got) != 1 || got[0] != atDesk {
		t.Fatalf("the fixture did not adopt the file: %v", got)
	}
	if !stands() {
		t.Fatal("a voice named in the file was adopted by the configuration and the rule did not hear of it")
	}

	// Taken out of the file: the reload must end it, with no restart.
	removed := a.configSnapshot()
	removed.Speech.OperatorSpeakers = nil
	if _, err := saveConfig(&removed); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Speech.OperatorSpeakers; len(got) != 0 {
		t.Fatalf("the fixture did not adopt the file: %v", got)
	}
	if stands() {
		t.Fatal("a voice removed from the file still stands as the operator's word after the reload")
	}
}
