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

// .
// .
// .
// .
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
	if err := say("twelve chars"); err != nil {
		t.Fatal(err)
	}
	if err := say("and thirteen!"); err != nil {
		t.Fatal(err)
	}
	if chars, _ := a.speechSpent("tts"); chars != 25 {
		t.Fatalf("what was spoken was not counted: %d characters", chars)
	}
	spend := a.speechSpend()
	if len(spend) != 1 || spend[0].Provider != "my-voice" || spend[0].Direction != "tts" || spend[0].Requests != 2 || spend[0].Characters != 25 {
		t.Fatalf("the meter does not read per service: %+v", spend)
	}

	// .
	// .
	if err := say("twelve chars"); err != nil {
		t.Fatal(err)
	}
	if chars, _ := a.speechSpent("tts"); chars != 25 || asked.Load() != 2 {
		t.Fatalf("a remembered reply was counted again: %d characters over %d requests", chars, asked.Load())
	}

	// .
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
	// .
	if err := say("fits"); err != nil {
		t.Fatalf("a reply inside the ceiling was refused: %v", err)
	}
}

// .
// .
// .
func TestListeningIsCountedInSecondsAndHeldToTheCeiling(t *testing.T) {
	srv := fakeEngine(t, "the kettle is on")
	defer srv.Close()
	a := speechApp(t, srv.URL)
	// .
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false); err != nil {
		t.Fatal(err)
	}
	// .
	// .
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
	// .
	err := a.HearUtterance(context.Background(), make([]byte, 2*16000*61), 16000, 1, false)
	if err == nil || !strings.Contains(err.Error(), "ceiling for the microphone") {
		t.Fatalf("the ceiling did not hold: %v", err)
	}
	if _, heard := a.speechSpent("stt"); heard != 200*time.Millisecond {
		t.Fatalf("a refusal was billed: %s", heard)
	}
	// .
	// .
	state, reason, _ := a.VoiceStatus()
	if state != "unreachable" || !strings.Contains(reason, "ceiling") {
		t.Fatalf("the microphone did not say why it stopped: %s / %s", state, reason)
	}
}

// .
// .
// .
func TestTheMonthIsTheOperatorsOwn(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Timezone = "Pacific/Auckland"
	// .
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

// .
// .
// .
// .
func TestACeilingIsSavedWithoutAskingTheService(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	// .
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"http://127.0.0.1:9","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "m", Voice: "v"}
	if _, err := a.applyConfigChange(map[string]interface{}{"speech.tts.monthly_characters": 50000}); err != nil {
		t.Fatalf("a ceiling was refused because a service did not answer: %v", err)
	}
	if got := a.configSnapshot().Speech.TTS.MonthlyCharacters; got != 50000 {
		t.Fatalf("the ceiling did not apply: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]interface{}{"speech.stt.monthly_minutes": 120}); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.STT.MonthlyMinutes; got != 120 {
		t.Fatalf("the listening ceiling did not apply: %d", got)
	}
	// .
	if _, err := a.applyConfigChange(map[string]interface{}{"speech.tts.monthly_characters": 0}); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.TTS.MonthlyCharacters; got != 0 {
		t.Fatalf("a cleared ceiling was left behind: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]interface{}{"speech.tts.monthly_characters": -1}); err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("a negative ceiling was taken: %v", err)
	}
	// .
	st := a.configState()
	if st.Speech.STT.MonthlyMinutes != 120 {
		t.Fatalf("the readback does not carry the ceiling: %+v", st.Speech.STT)
	}
	if st.Speech.Resets == "" {
		t.Fatal("the readback does not say when the meter resets")
	}
}
