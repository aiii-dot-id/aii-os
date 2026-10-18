package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// A REPLY SPEAKS WITH THE SERVICE ITS MINT CHECKED (T22.4). The mint
// settles everything that can be known without asking the service — the
// entry, its key, the synthesizer — and the synthesis used to throw that
// away and read providers.json again when it started. A Settings → Speech
// save landing between the two then spoke with an entry and a key the
// mint never checked. The mint now carries its synthesizer: the reply is
// spoken where it was checked, with the key it was checked with, and
// providers.json is not read again; the key goes with the synthesis.
func TestAReplySpeaksWithTheServiceItsMintChecked(t *testing.T) {
	serve := func(asked *atomic.Int32, auth *atomic.Value) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked.Add(1)
			auth.Store(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "audio/pcm")
			_, _ = w.Write(samples(240))
		}))
	}
	var askedA, askedB atomic.Int32
	var authA, authB atomic.Value
	checked, saved := serve(&askedA, &authA), serve(&askedB, &authB)
	defer checked.Close()
	defer saved.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	write := func(url, key string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+url+`","api_key":"`+key+`"}]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(checked.URL, "vk-checked")
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "tts-1", Voice: "alba"}

	r, err := a.mint(dashboard.SpeakText{Text: "the kettle is on"})
	if err != nil {
		t.Fatal(err)
	}
	// The save lands between the mint and the start.
	write(saved.URL, "vk-saved")
	if _, err := a.spokenAudio(t.Context(), r.id); err != nil {
		t.Fatal(err)
	}
	if askedA.Load() != 1 || askedB.Load() != 0 {
		t.Fatalf("the reply was spoken by %d checked and %d unchecked requests: the synthesis read providers.json again", askedA.Load(), askedB.Load())
	}
	if got, _ := authA.Load().(string); got != "Bearer vk-checked" {
		t.Fatalf("the reply was spoken with a key its mint did not check: %q", got)
	}
	r.mu.Lock()
	syn, key := r.syn, r.say.APIKey
	r.mu.Unlock()
	if syn != nil || key != "" {
		t.Fatal("the synthesizer, and the key it carries, outlived the synthesis")
	}
}
