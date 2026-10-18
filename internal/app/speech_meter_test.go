package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// WHAT THE VOICE COSTS IS COUNTED WHERE IT IS SPENT, per service, and a
// ceiling refuses before the request rather than after the bill. Cloud
// speech is billed by the character and the second — a currency no token
// counter shows.
func TestSpeakingIsCountedPerServiceAndHeldToTheCeiling(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(samples(40))
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "m", Voice: "v"}
	say := func(text string) error {
		_, err := spoken(t, a, dashboard.SpeakText{Text: text})
		return err
	}
	if err := say("twelve chars"); err != nil { // 12
		t.Fatal(err)
	}
	if err := say("and thirteen!"); err != nil { // 13
		t.Fatal(err)
	}
	if chars, _ := a.speechSpent("tts"); chars != 25 {
		t.Fatalf("what was spoken was not counted: %d characters", chars)
	}
	spend := a.speechSpend()
	if len(spend) != 1 || spend[0].Provider != "my-voice" || spend[0].Direction != "tts" || spend[0].Requests != 2 || spend[0].Characters != 25 {
		t.Fatalf("the meter does not read per service: %+v", spend)
	}

	// THE SAME REPLY AGAIN COSTS NOTHING AND COUNTS NOTHING: it is not
	// bought twice, so it must not be billed twice either.
	if err := say("twelve chars"); err != nil {
		t.Fatal(err)
	}
	if chars, _ := a.speechSpent("tts"); chars != 25 || asked.Load() != 2 {
		t.Fatalf("a remembered reply was counted again: %d characters over %d requests", chars, asked.Load())
	}

	// A CEILING REFUSES BEFORE THE REQUEST, and says when it lifts.
	a.cfg.Speech.TTS.MonthlyCharacters = 30
	before := asked.Load()
	err := say("this line would take it past the ceiling")
	if err == nil || !strings.Contains(err.Error(), "ceiling for spoken replies") {
		t.Fatalf("the ceiling did not hold: %v", err)
	}
	if asked.Load() != before {
		t.Fatal("a refused reply was still sent to the service")
	}
	if chars, _ := a.speechSpent("tts"); chars != 25 {
		t.Fatalf("a refusal was billed: %d characters", chars)
	}
	// AND WHAT STILL FITS IS STILL SPOKEN.
	if err := say("fits"); err != nil {
		t.Fatalf("a reply inside the ceiling was refused: %v", err)
	}
}

// LISTENING IS COUNTED IN THE TIME IT ACTUALLY TOOK, and its ceiling
// refuses with the audio still on this machine — a ceiling that only
// refused afterwards would have already sent the room to a third party.
func TestListeningIsCountedInSecondsAndHeldToTheCeiling(t *testing.T) {
	srv := fakeEngine(t, "the kettle is on")
	defer srv.Close()
	a := speechApp(t, srv.URL)
	// 3200 bytes of 16 kHz mono is a tenth of a second.
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false); err != nil {
		t.Fatal(err)
	}
	// The same tenth of a second in stereo is twice the samples and the
	// same amount of listening: a bill is in seconds, not in bytes.
	if err := a.HearUtterance(context.Background(), make([]byte, 6400), 16000, 2, false); err != nil {
		t.Fatal(err)
	}
	_, heard := a.speechSpent("stt")
	if heard != 200*time.Millisecond {
		t.Fatalf("what was heard was not counted: %s", heard)
	}
	spend := a.speechSpend()
	if len(spend) != 1 || spend[0].Direction != "stt" || spend[0].Requests != 2 {
		t.Fatalf("the meter does not read per service: %+v", spend)
	}
	a.cfg.Speech.STT.MonthlyMinutes = 1
	// A minute of audio at 16 kHz mono, which the ceiling will not cover.
	err := a.HearUtterance(context.Background(), make([]byte, 2*16000*61), 16000, 1, false)
	if err == nil || !strings.Contains(err.Error(), "ceiling for the microphone") {
		t.Fatalf("the ceiling did not hold: %v", err)
	}
	if _, heard := a.speechSpent("stt"); heard != 200*time.Millisecond {
		t.Fatalf("a refusal was billed: %s", heard)
	}
	// THE MICROPHONE SAYS WHY, rather than inviting the operator to speak
	// into a service that will refuse them.
	state, reason, _ := a.VoiceStatus()
	if state != "unreachable" || !strings.Contains(reason, "ceiling") {
		t.Fatalf("the microphone did not say why it stopped: %s / %s", state, reason)
	}
}

// THE MONTH TURNS OVER AT THE OPERATOR'S MIDNIGHT, not at UTC's: a
// ceiling that lifted in the middle of their evening would be someone
// else's month.
func TestTheMonthIsTheOperatorsOwn(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Timezone = "Pacific/Auckland"
	// 21:00 UTC on 30 September is 10:00 on 1 October in Auckland.
	at := time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC)
	if got := a.speechMonth(at); got != "2026-10" {
		t.Errorf("the month is not the operator's: %s", got)
	}
	if got := a.speechResets(at).Format("2006-01-02"); got != "2026-11-01" {
		t.Errorf("the meter resets on the wrong day: %s", got)
	}
	a.cfg.Timezone = ""
	if got := a.speechMonth(at); got != "2026-09" {
		t.Errorf("with no timezone the month is UTC's: %s", got)
	}
	a.cfg.Timezone = "Nowhere/Nothing"
	if got := a.speechMonth(at); got != "2026-09" {
		t.Errorf("an unknown timezone must fall back to UTC, not to nothing: %s", got)
	}
}

// A CEILING IS NOT A POINTER. It changes what this identity is willing to
// spend, not where it speaks, so it is saved on its own word — with no
// service asked to answer for it, which matters most exactly when the
// service is the thing that has gone wrong.
func TestACeilingIsSavedWithoutAskingTheService(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	// An address nothing answers at: a live check here could only fail.
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"http://127.0.0.1:9","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "m", Voice: "v"}
	if _, err := a.applyConfigChange(map[string]any{"speech.tts.monthly_characters": 50000}); err != nil {
		t.Fatalf("a ceiling was refused because a service did not answer: %v", err)
	}
	if got := a.configSnapshot().Speech.TTS.MonthlyCharacters; got != 50000 {
		t.Fatalf("the ceiling did not apply: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"speech.stt.monthly_minutes": 120}); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.STT.MonthlyMinutes; got != 120 {
		t.Fatalf("the listening ceiling did not apply: %d", got)
	}
	// EMPTY IS NO CEILING, and a negative one is refused by name.
	if _, err := a.applyConfigChange(map[string]any{"speech.tts.monthly_characters": 0}); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.TTS.MonthlyCharacters; got != 0 {
		t.Fatalf("a cleared ceiling was left behind: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"speech.tts.monthly_characters": -1}); err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("a negative ceiling was taken: %v", err)
	}
	// AND THE READBACK CARRIES IT, so the card shows what is in force.
	st := a.configState()
	if st.Speech.STT.MonthlyMinutes != 120 {
		t.Fatalf("the readback does not carry the ceiling: %+v", st.Speech.STT)
	}
	if st.Speech.Resets == "" {
		t.Fatal("the readback does not say when the meter resets")
	}
}
