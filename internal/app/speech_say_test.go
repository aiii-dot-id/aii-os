package app

import (
	"context"
	"encoding/binary"
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
func samples(n int) []byte {
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(i*7))
	}
	return b
}

// .
// .
// .
// .
// .
func TestAReplyIsSpokenByTheServiceTheOperatorChose(t *testing.T) {
	var asked atomic.Int32
	var body atomic.Value
	body.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(samples(240))
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "tts-1", Voice: "alba"}

	if got := a.replyVoice(); got != "my-voice" {
		t.Fatalf("the page was not told which service speaks: %q", got)
	}
	wav, err := spoken(t, a, dashboard.SpeakText{Text: "the kettle is on"})
	if err != nil {
		t.Fatal(err)
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Fatalf("this is not audio a page can play: %q", wav[:44])
	}
	if ch := binary.LittleEndian.Uint16(wav[22:24]); ch != 1 {
		t.Errorf("channels %d", ch)
	}
	if rate := binary.LittleEndian.Uint32(wav[24:28]); rate != 24000 {
		t.Errorf("the rate the service spoke at is not in the header: %d", rate)
	}
	if bits := binary.LittleEndian.Uint16(wav[34:36]); bits != 16 {
		t.Errorf("bits a sample %d", bits)
	}
	// .
	// .
	// .
	if n := binary.LittleEndian.Uint32(wav[40:44]); n != 0xFFFFFFFF {
		t.Errorf("a stream claimed a length: %d", n)
	}
	if len(wav) != 44+480 {
		t.Errorf("the samples did not survive the header: %d bytes", len(wav))
	}
	if sent := body.Load().(string); !strings.Contains(sent, "the kettle is on") {
		t.Errorf("the reply is not what was asked for: %s", sent)
	}

	// .
	again, err := spoken(t, a, dashboard.SpeakText{Text: "the kettle is on"})
	if err != nil || len(again) != len(wav) || asked.Load() != 1 {
		t.Fatalf("the same reply was bought %d times (%v)", asked.Load(), err)
	}
	// .
	if _, err := spoken(t, a, dashboard.SpeakText{Text: "the kettle is off"}); err != nil || asked.Load() != 2 {
		t.Fatalf("a new reply was not spoken: %d asks, %v", asked.Load(), err)
	}
}

// .
// .
func TestWithNoSpeakingServiceTheBrowsersVoiceKeepsTheReply(t *testing.T) {
	a := newVoiceApp(t)
	if got := a.replyVoice(); got != "" {
		t.Fatalf("a page was told a service speaks when none does: %q", got)
	}
	if _, err := spoken(t, a, dashboard.SpeakText{Text: "the kettle is on"}); err == nil || !strings.Contains(err.Error(), "no speaking service") {
		t.Fatalf("refusal %v", err)
	}
}

// .
// .
func TestASpokenRefusalCarriesTheServicesWordsAndNotItsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"this voice is not on your plan"}}`))
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-not-in-an-error"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "tts-1", Voice: "alba"}
	_, err := spoken(t, a, dashboard.SpeakText{Text: "the kettle is on"})
	if err == nil || !strings.Contains(err.Error(), "not on your plan") {
		t.Fatalf("the service's own words did not come back: %v", err)
	}
	if strings.Contains(err.Error(), "vk-not-in-an-error") {
		t.Fatalf("the key travelled in a refusal: %v", err)
	}
}

// .
// .
func TestAReplyTooLongToSpeakIsRefusedWhole(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(samples(8))
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "tts-1", Voice: "alba"}
	if _, err := spoken(t, a, dashboard.SpeakText{Text: strings.Repeat("a", sayBoundChars+1)}); err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("refusal %v", err)
	}
	if asked.Load() != 0 {
		t.Fatalf("a refused reply was still sent to the service %d times", asked.Load())
	}
}

// .
// .
// .
// .
// .
func TestASampleSpeaksTheVoiceBeingPickedWithoutKeepingIt(t *testing.T) {
	var asked atomic.Int32
	var saw atomic.Value
	saw.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		b, _ := io.ReadAll(r.Body)
		saw.Store(r.Header.Get("Authorization") + " " + string(b))
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(samples(120))
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`"},{"name":"in-force","url":"`+srv.URL+`","api_key":"saved-key"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "in-force", Model: "saved-model", Voice: "saved-voice"}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wav, err := spoken(t, a, dashboard.SpeakText{Sample: true,
		Provider: "my-voice", Model: "tts-1", Voice: "alba", APIKey: "only-typed-1234"})
	if err != nil {
		t.Fatal(err)
	}
	if string(wav[0:4]) != "RIFF" || len(wav) != 44+240 {
		t.Fatalf("the sample is not playable audio: %d bytes", len(wav))
	}
	sent := saw.Load().(string)
	if !strings.Contains(sent, "only-typed-1234") || !strings.Contains(sent, `"voice":"alba"`) || !strings.Contains(sent, `"model":"tts-1"`) {
		t.Fatalf("the candidate on the card is not what spoke: %s", sent)
	}
	if !strings.Contains(sent, sampleWords) {
		t.Fatalf("the sample said something other than the host's own words: %s", sent)
	}
	if strings.Contains(sent, "saved-key") || strings.Contains(sent, "saved-voice") {
		t.Fatalf("the saved service spoke instead of the candidate: %s", sent)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a sample wrote something down:\n%s", after)
	}
	// .
	// .
	if _, err := spoken(t, a, dashboard.SpeakText{Sample: true,
		Provider: "my-voice", Model: "tts-1", Voice: "alba", APIKey: "only-typed-1234"}); err != nil || asked.Load() != 2 {
		t.Fatalf("a second sample was answered from the memo: %d asks, %v", asked.Load(), err)
	}
	// .
	if _, err := spoken(t, a, dashboard.SpeakText{Text: "the kettle is on"}); err != nil {
		t.Fatal(err)
	}
	if sent := saw.Load().(string); !strings.Contains(sent, "saved-key") || !strings.Contains(sent, `"voice":"saved-voice"`) {
		t.Fatalf("a reply was spoken by the candidate: %s", sent)
	}
}

// .
// .
func TestASampleWithNoKeySaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := spoken(t, a, dashboard.SpeakText{Sample: true, Provider: "my-voice", Model: "tts-1", Voice: "alba"})
	if err == nil || !strings.Contains(err.Error(), "no API key is stored for my-voice") {
		t.Fatalf("a keyless sample did not say what to do: %v", err)
	}
	if !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Fatalf("the service's own words were dropped: %v", err)
	}
}

// .
// .
// .
// .
func TestAShippedVoiceNeedsNoEntryAndIsNotAskedWithoutAKey(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"Local","url":"http://127.0.0.1:9"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	shipped := speechEntryNamed(&providerRegistry{}, "Cartesia")
	if shipped == nil {
		t.Skip("no shipped service to stand for this rule")
	}
	if shipped.APIKeyEnv != "" {
		t.Setenv(shipped.APIKeyEnv, "")
	}
	_, err := spoken(t, a, dashboard.SpeakText{Sample: true, Provider: "Cartesia", Voice: "c-2"})
	if err == nil || !strings.Contains(err.Error(), "no API key is stored for Cartesia") {
		t.Fatalf("a shipped voice with no key: %v", err)
	}
}

// .
// .
func spoken(t *testing.T, a *App, say dashboard.SpeakText) ([]byte, error) {
	t.Helper()
	id, err := a.speakMint(say)
	if err != nil {
		return nil, err
	}
	return a.spokenAudio(context.Background(), id)
}

// .
// .
// .
// .
// .
// .
// .
func TestAReplyStartsPlayingBeforeItIsFinished(t *testing.T) {
	const piece = 250 * time.Millisecond
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		_, _ = io.ReadAll(r.Body)
		time.Sleep(piece)
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

	// .
	reply := "The kettle is on and it will be a minute or two. " +
		"I put the good tea in it, the one from the tin at the back, because you said the other was too grassy. " +
		"There is a biscuit as well, if you want it, though I think the tin is nearly out and I keep forgetting. " +
		"The window is open, so it may be a little cold in there, and the cat has taken the warm chair again. " +
		"Come through when you are ready and I will pour it."
	id, err := a.speakMint(dashboard.SpeakText{Text: reply})
	if err != nil {
		t.Fatal(err)
	}
	w := &timedWriter{start: time.Now()}
	if err := a.speakPlay(context.Background(), id, w); err != nil {
		t.Fatal(err)
	}
	if asked.Load() < 2 {
		t.Fatalf("the reply was spoken in one request, so nothing could start early: %d", asked.Load())
	}
	first, last := w.first, w.last
	if first > 2*piece {
		t.Errorf("the first audio waited for %s — about %d pieces — instead of one", first.Round(time.Millisecond), int(first/piece))
	}
	if last < time.Duration(asked.Load()-1)*piece {
		t.Errorf("the whole reply arrived too soon to have been made in pieces: %s", last)
	}
	if last <= first {
		t.Errorf("nothing arrived after the opening: first %s, last %s", first, last)
	}
}

// .
type timedWriter struct {
	start time.Time
	first time.Duration
	last  time.Duration
	n     int
}

func (t *timedWriter) Write(p []byte) (int, error) {
	if t.n == 0 {
		t.first = time.Since(t.start)
	}
	t.last = time.Since(t.start)
	t.n += len(p)
	return len(p), nil
}
