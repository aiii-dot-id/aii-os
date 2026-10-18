package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// speech_test.go — spoken audio becomes something the identity read.
//
// THIS IS THE TEST THE PREVIOUS DESIGN NEVER HAD. Three thousand lines
// of audio transport shipped green because every test proved a component
// worked in isolation; none put audio in at one end and required words
// at the other. So this hands real PCM to the real path, against a real
// (if local) HTTP engine, and requires the identity's conversation to
// contain what was said.

// fakeEngine is an OpenAI-shaped transcription endpoint.
func fakeEngine(t *testing.T, text string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, _, err := r.FormFile("file"); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	}))
}

// speechApp is newVoiceApp plus a providers.json and a speech pointer
// into it — the configuration an operator would actually write.
func speechApp(t *testing.T, endpoint string) *App {
	t.Helper()
	a := newVoiceApp(t)
	dir := filepath.Dir(a.cfg.SourcePath)
	providers := map[string]any{
		"providers": []map[string]any{
			{"name": "local-speech", "url": endpoint, "default_model": "whisper-1"},
		},
	}
	blob, err := json.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.STT = STTConfig{Provider: "local-speech", Model: "whisper-1"}
	return a
}

// Silence is not an error and not a turn. An operator who taps the
// microphone and says nothing has done nothing wrong.
func TestSilenceEntersNothing(t *testing.T) {
	srv := fakeEngine(t, "   ")
	defer srv.Close()
	a := speechApp(t, srv.URL)

	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false); err != nil {
		t.Fatalf("silence was treated as a failure: %v", err)
	}
	turns, _ := a.store.RecentTurns(10)
	for _, turn := range turns {
		if turn.Role == "participant" {
			t.Fatalf("silence became a turn: %q", turn.Content)
		}
	}
}

// AN UNCONFIGURED IDENTITY SAYS SO. The operator must be able to tell a
// missing endpoint from a broken one — and the page must not offer a
// microphone that cannot work.
func TestAnIdentityWithNoSpeechEndpointSaysSo(t *testing.T) {
	a := newVoiceApp(t)
	if a.VoiceConfigured() {
		t.Fatal("an identity with no speech config offered a microphone")
	}
	err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false)
	if err == nil {
		t.Fatal("an unconfigured identity silently accepted audio")
	}
	if !strings.Contains(err.Error(), "speech.stt") {
		t.Fatalf("the refusal does not say what to configure: %v", err)
	}
}

// A POINTER THAT DOES NOT DEREFERENCE IS A TYPO, and a typo that
// silently disables the microphone is worse than one that says so.
func TestASpeechPointerToNowhereIsAnError(t *testing.T) {
	a := speechApp(t, "http://127.0.0.1:1")
	a.cfg.Speech.STT.Provider = "not-in-providers"
	if a.VoiceConfigured() {
		t.Fatal("a broken pointer was reported as a working microphone")
	}
	err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false)
	if err == nil || !strings.Contains(err.Error(), "not-in-providers") {
		t.Fatalf("the error does not name the missing provider: %v", err)
	}
}

// And an endpoint that is not there reaches the operator as itself,
// rather than as silence.
func TestAnEndpointThatIsNotThereIsReported(t *testing.T) {
	a := speechApp(t, "http://127.0.0.1:1")
	if !a.VoiceConfigured() {
		t.Fatal("a configured endpoint was reported as unconfigured before it was tried")
	}
	err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false)
	if err == nil {
		t.Fatal("a dead endpoint returned a transcription")
	}
}
