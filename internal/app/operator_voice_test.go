package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestTheStoresSpokenMarkerIsTheHostsVoiceMarker(t *testing.T) {
	if store.SpokenMarker != voiceMarker {
		t.Fatalf("the store reads a spoken turn by %q, the host marks one with %q", store.SpokenMarker, voiceMarker)
	}
	if store.AnnotationSpeaker != annotationSpeaker || store.AnnotationVoice != annotationVoice {
		t.Fatal("the annotation kinds the store reads are not the ones the host writes")
	}
}

func TestTheOperatorsVoicesAreReadWholeOrRefused(t *testing.T) {
	const (
		atDesk  = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
		onPhone = "b3a1f7d2-52c4-4e0b-8d19-7e6f5a4b3c2d"
	)
	nine := make([]any, 9)
	for i := range nine {
		nine[i] = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5" + string(rune('0'+i))
	}
	for name, c := range map[string]struct {
		change  any
		want    string
		refusal string
	}{
		"one voice":            {[]any{atDesk}, atDesk, ""},
		"two voices":           {[]any{atDesk, onPhone}, atDesk + " " + onPhone, ""},
		"null":                 {nil, "", ""},
		"an empty list":        {[]any{}, "", ""},
		"not a list":           {atDesk, "", "a list of speaker UUIDs"},
		"an enrolled id":       {[]any{"quinn-desk"}, "", "is not a speaker UUID"},
		"a UUID in capitals":   {[]any{strings.ToUpper(atDesk)}, "", "is not a speaker UUID"},
		"the nil UUID":         {[]any{"00000000-0000-0000-0000-000000000000"}, "", "is not a speaker UUID"},
		"a UUID twice":         {[]any{atDesk, atDesk}, "", "listed twice"},
		"too many":             {nine, "", "the most that are one operator's"},
		"a UUID with a suffix": {[]any{atDesk + "\n"}, "", "is not a speaker UUID"},
	} {
		got, err := operatorSpeakersFromChange(c.change)
		switch {
		case c.refusal == "" && (err != nil || strings.Join(got, " ") != c.want):
			t.Errorf("%s: got %v, %v; want %q", name, got, err, c.want)
		case c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)):
			t.Errorf("%s: want a refusal saying %q, got %v, %v", name, c.refusal, got, err)
		}
	}
}

func TestNamingTheOperatorsVoiceReachesTheStore(t *testing.T) {
	const (
		atDesk  = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
		onPhone = "b3a1f7d2-52c4-4e0b-8d19-7e6f5a4b3c2d"
	)
	a := newVoiceApp(t)
	a.voiceSessions.Store("vs-1", &voiceHandle{id: "vs-1"})
	say := func(words string, final int64, observed pluginhost.Event) uint64 {
		t.Helper()
		seq, err := a.store.AddConversationTurnSeq(roleOperator, voiceMarker+words)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.store.AnnotateTurn(seq, annotationVoice, voiceRefKey("vs-1", final), `{"session":"vs-1"}`); err != nil {
			t.Fatal(err)
		}
		a.noteSpeakerObservation(decodeVoiceFrame(observed), false)
		return seq
	}
	stands := func(seq uint64) bool {
		t.Helper()
		turn, err := a.store.LatestOperatorWord()
		var waiting *store.UnconfirmedSpeechError
		return turn == nil && errors.As(err, &waiting) && waiting.TurnSeq == seq
	}
	seq := say("yes, record it", 7, knownUUIDObservation("vs-1", 20, 7, "t1", "quinn-desk", atDesk, "matched"))
	if stands(seq) {
		t.Fatal("a spoken turn stood as the operator's word with no voice named")
	}
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{"quinn-desk"}}); err == nil {
		t.Fatal("an enrolled speaker's id was taken as the operator's voice")
	}
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{onPhone}}); err != nil {
		t.Fatalf("naming a voice: %v", err)
	}
	if stands(seq) {
		t.Fatal("the turn stood on a UUID that is not its speaker's")
	}
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{onPhone, atDesk}}); err != nil {
		t.Fatalf("naming the operator's voice: %v", err)
	}
	if !stands(seq) {
		t.Fatal("the speaker's UUID was named in the configuration and the rule did not hear of it")
	}
	if got := a.configSnapshot().Speech.OperatorSpeakers; len(got) != 2 || got[1] != atDesk {
		t.Fatalf("the configuration holds %v", got)
	}
	// The same speaker, the same enrolled name, and a UUID the engine did
	// not find by a match: recorded by the same path, and it does not
	// stand, whether the engine has not settled it or has just made it.
	for i, continuity := range []string{"provisional", "new_profile"} {
		unmatched := say("and this too", int64(8+i), knownUUIDObservation("vs-1", int64(21+i), int64(8+i), "t1", "quinn-desk", atDesk, continuity))
		if stands(unmatched) || stands(seq) {
			t.Fatalf("a %s UUID stood as the operator's voice, or an earlier word was cited past it", continuity)
		}
	}

	anonymous := say("and one more", 10, uuidObservation("vs-1", 23, 10, "t1", atDesk))
	if stands(anonymous) || stands(seq) {
		t.Fatal("an anonymous speaker's UUID stood as the operator's identified voice, or an earlier word was cited past it")
	}
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": nil}); err != nil {
		t.Fatalf("un-naming the operator's voice: %v", err)
	}
	if _, err := a.store.AddConversationTurnSeq(roleOperator, "typed: never mind"); err != nil {
		t.Fatal(err)
	}
	again := say("yes, record it", 12, knownUUIDObservation("vs-1", 25, 12, "t1", "quinn-desk", atDesk, "matched"))
	if stands(again) {
		t.Fatal("the name was taken away and a spoken turn still stands")
	}
}

func TestTheYourVoiceCardListsTheVoicesHeardAndTheOnesNamed(t *testing.T) {
	const (
		theirs  = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
		guest   = "9078c44a-6fbe-4f24-a20e-bd212f520530"
		unheard = "b3a1f7d2-52c4-4e0b-8d19-7e6f5a4b3c2d"
	)
	a := newVoiceApp(t)
	a.voiceSessions.Store("vs-1", &voiceHandle{id: "vs-1"})
	heard := func(final int64, observed pluginhost.Event) {
		t.Helper()
		seq, err := a.store.AddConversationTurnSeq(roleParticipant, voiceMarker+"words")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.store.AnnotateTurn(seq, annotationVoice, voiceRefKey("vs-1", final), `{"session":"vs-1"}`); err != nil {
			t.Fatal(err)
		}
		a.noteSpeakerObservation(decodeVoiceFrame(observed), false)
	}
	heard(7, knownUUIDObservation("vs-1", 20, 7, "t1", "op-desk", theirs, "matched"))
	heard(8, uuidObservation("vs-1", 21, 8, "t2", guest))
	voices := a.configState().Speech.Voices
	if len(voices) != 2 || voices[0].UUID != guest || voices[1].UUID != theirs {
		t.Fatalf("the voices the record heard, newest first: %+v", voices)
	}
	if v := voices[1]; !v.Enrolled || v.Mine || v.WhyNot != "" || v.Label != "Label op-desk" || !v.Heard {
		t.Fatalf("an enrolled voice: %+v", v)
	}
	if v := voices[0]; v.Enrolled || v.WhyNot != voiceNotEnrolled {
		t.Fatalf("a voice that is not enrolled does not say why it cannot count: %+v", v)
	}
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{theirs, unheard}}); err != nil {
		t.Fatalf("naming the operator's voices through the settings door: %v", err)
	}
	voices = a.configState().Speech.Voices
	if len(voices) != 3 || !voices[1].Mine || voices[0].Mine || voices[2] != (dashboard.SpeakerVoice{UUID: unheard, Mine: true, WhyNot: voiceNotHeard}) {
		t.Fatalf("the named voices: %+v", voices)
	}
	if why := a.store.OperatorVoiceWhyNot(`{"decision":"known","speaker_id":"op-desk","speaker_uuid":"` + theirs + `","continuity":"matched"}`); why != "" {
		t.Fatalf("a voice named on the card does not reach the rule: %s", why)
	}
}
