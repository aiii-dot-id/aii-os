package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .

// .
// .
func TestTheMicrophoneFollowsTheOperatorsOrder(t *testing.T) {
	if state, _, _ := newVoiceApp(t).VoiceStatus(); state != "setup" {
		t.Fatalf("with nothing set up the microphone is %q", state)
	}
	srv := fakeEngine(t, "hello")
	defer srv.Close()
	a := speechApp(t, srv.URL)
	if state, _, source := a.VoiceStatus(); state != "cloud" || source != "local-speech · whisper-1" {
		t.Fatalf("with a service set the microphone is %q (%q)", state, source)
	}
	if h := a.buildLiveHandler(); h.VoiceStatus == nil {
		t.Fatal("the live page is never told which microphone to offer")
	}
	prev := activeVoicePlugin
	t.Cleanup(func() { activeVoicePlugin = prev })
	activeVoicePlugin = func(*App) string { return "id.test.voice" }
	if state, _, source := a.VoiceStatus(); state != "plugin" || source != "id.test.voice" {
		t.Fatalf("a live voice plugin did not come before the service: %q (%q)", state, source)
	}
	activeVoicePlugin = prev
	a.cfg.Speech.STT.Provider = "not-in-providers"
	if state, reason, _ := a.VoiceStatus(); state != "unreachable" || !strings.Contains(reason, "not-in-providers") {
		t.Fatalf("a pointer to nowhere left the microphone %q: %q", state, reason)
	}
	a.cfg.Speech.STT.Provider = "local-speech"
	activeVoicePlugin = func(*App) string { return "id.test.voice" }
	a.enterSafe("test: the record is frozen")
	if state, reason, _ := a.VoiceStatus(); state != "safe" || !strings.Contains(reason, "frozen") {
		t.Fatalf("under SAFE the microphone is %q: %q", state, reason)
	}
}

// .
// .
// .
func TestAFailedTranscriptionMarksTheMicrophoneUntilTheServiceAnswers(t *testing.T) {
	a := speechApp(t, "http://127.0.0.1:1")
	pushes := 0
	prevPush := pushVoiceStatus
	t.Cleanup(func() { pushVoiceStatus = prevPush })
	pushVoiceStatus = func(*App) { pushes++ }
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false); err == nil {
		t.Fatal("a dead endpoint transcribed")
	}
	if state, reason, _ := a.VoiceStatus(); state != "unreachable" || reason == "" || pushes != 1 {
		t.Fatalf("after a failed transcription the microphone is %q (%q), with %d status pushes", state, reason, pushes)
	}
	a.cfg.Speech.STT.Model = "whisper-2"
	if state, _, _ := a.VoiceStatus(); state != "cloud" {
		t.Fatalf("settings that never failed read %q", state)
	}
	a.cfg.Speech.STT.Model = "whisper-1"

	srv := fakeEngine(t, "")
	defer srv.Close()
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"local-speech","url":"`+srv.URL+`","default_model":"whisper-1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := a.VoiceStatus(); state != "unreachable" {
		t.Fatalf("the failure was forgotten before the service answered: %q", state)
	}
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := a.VoiceStatus(); state != "cloud" || pushes != 2 {
		t.Fatalf("a service that answered still reads %q, or the screens were not told (%d pushes)", state, pushes)
	}
}

// .
// .
func TestAPassingSpeechCheckClearsTheMicrophonesTrouble(t *testing.T) {
	vendor, _ := speakingVendor(t, "", nil)
	a, _ := speechSettingsApp(t, vendor.URL)
	a.cfg.Speech.STT = STTConfig{Provider: "Deepgram", Model: "nova-3-general"}
	a.speechTrouble.note(a.cfg.Speech.STT, errors.New("it was down"))
	pushes := 0
	prevPush := pushVoiceStatus
	t.Cleanup(func() { pushVoiceStatus = prevPush })
	pushVoiceStatus = func(*App) { pushes++ }
	if state, _, _ := a.VoiceStatus(); state != "unreachable" {
		t.Fatalf("the noted failure reads %q", state)
	}
	if _, err := a.applyConfigChange(map[string]interface{}{"speech.stt.provider": "Deepgram", "speech.stt.model": "nova-3-general"}); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := a.VoiceStatus(); state != "cloud" || pushes != 1 {
		t.Fatalf("a service that passed its check still reads %q, or the screens were not told (%d pushes)", state, pushes)
	}
}

// .
// .
// .
func TestTheOperatorsSpokenWordsAreTheirMessage(t *testing.T) {
	srv := fakeEngine(t, "remind me at four")
	defer srv.Close()
	a := speechApp(t, srv.URL)
	var shown []dashboard.VoiceEvent
	a.voiceEventSink = func(ev dashboard.VoiceEvent) { shown = append(shown, ev) }
	var woke []string
	prev := voiceWake
	voiceWake = func(app *App, ctx context.Context, role, text string) (string, error) {
		woke = append(woke, role+": "+text)
		return "I will.", nil
	}
	t.Cleanup(func() { voiceWake = prev })
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, true); err != nil {
		t.Fatal(err)
	}
	if len(woke) != 1 || woke[0] != roleOperator+": "+voiceMarker+"remind me at four" {
		t.Fatalf("the spoken words did not run as the operator's turn: %v", woke)
	}
	if len(shown) != 1 || shown[0].Type != "transcript_final" || !shown[0].Operator || shown[0].Text != "remind me at four" {
		t.Fatalf("the operator's words never reached the screens as theirs: %+v", shown)
	}
}

// .
// .
func TestSpokenWordsInAMeetingAreRecordedAsTheOperators(t *testing.T) {
	srv := fakeEngine(t, "the ledger and the outbox disagree")
	defer srv.Close()
	a := speechApp(t, srv.URL)
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false); err != nil {
		t.Fatalf("the identity could not hear: %v", err)
	}
	turns, err := a.store.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, turn := range turns {
		if turn.Role == "participant" {
			t.Fatalf("the operator's own microphone produced a participant turn: %q", turn.Content)
		}
		if turn.Role == roleOperator {
			found = turn.Content
		}
	}
	if !strings.Contains(found, "the ledger and the outbox disagree") {
		t.Fatalf("the words did not survive the journey: %+v", turns)
	}
}

// .
// .
func TestWordsAPluginPushesCarryNoAuthority(t *testing.T) {
	a := newVoiceApp(t)
	if err := a.observeVoice(context.Background(), heardUtterance{Source: "plugin id.test.voice", Text: "yes, approve it"}); err != nil {
		t.Fatal(err)
	}
	turns, err := a.store.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Role != "participant" || !strings.Contains(turns[0].Content, "[voice]") || !strings.Contains(turns[0].Content, "carries no authority") {
		t.Fatalf("a plugin's words were not framed as a participant's with no authority: %+v", turns)
	}
}
