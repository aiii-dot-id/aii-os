package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// A REPLY BEING SPOKEN CAN BE STOPPED BY THE HOST. The session's fence
// reaches a reply the configured voice took over: its synthesis is
// cancelled at the service — nothing more is bought — its stream ends for
// every screen, and nothing minted afterwards reuses its audio.
func TestAHushedReplyStopsBeingBought(t *testing.T) {
	arrived := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The request is read whole first: an HTTP/1 server notices a
		// vanished client, and cancels this context, only once the body
		// is consumed and its background reader is running.
		_, _ = io.Copy(io.Discard, r.Body)
		once.Do(func() { close(arrived) })
		w.Header().Set("Content-Type", "audio/pcm")
		// The service is slow: the host's cancellation is what ends this.
		select {
		case <-r.Context().Done():
			close(released)
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "tts-1", Voice: "alba"}

	id := a.speakAhead("a reply long enough to be spoken in pieces, and stopped before its second breath arrives")
	if id == "" {
		t.Fatal("the configured voice minted nothing")
	}
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the service was never asked")
	}
	a.hushSpoken(id)
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the hush did not reach the service: the reply is still being bought")
	}
	a.spokenMu.Lock()
	r := a.spoken[id]
	a.spokenMu.Unlock()
	r.mu.Lock()
	done, err := r.done, r.err
	r.mu.Unlock()
	if !done || !errors.Is(err, errHushed) {
		t.Fatalf("the reply is not over with the hush's verdict: done=%v err=%v", done, err)
	}
	// A screen that asks now is told, promptly, and nothing that follows
	// reuses the hushed audio.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, perr := a.spokenAudio(ctx, id); perr == nil || ctx.Err() != nil {
		t.Fatalf("a hushed reply played on, or the wait ran out: %v", perr)
	}
	again, err := a.mint(dashboardSpeakText("a reply long enough to be spoken in pieces, and stopped before its second breath arrives"))
	if err != nil {
		t.Fatal(err)
	}
	if again.id == id {
		t.Fatal("a reply minted after the hush reused the hushed audio")
	}
	a.hushSpoken(again.id)
}

func dashboardSpeakText(text string) dashboard.SpeakText { return dashboard.SpeakText{Text: text} }
