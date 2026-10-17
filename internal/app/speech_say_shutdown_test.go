package app

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAReplysSynthesisInFlightIsJoinedBeforeTheStoreCloses(t *testing.T) {
	asked, answer, answered := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked <- struct{}{}
		<-answer
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(samples(240))
		close(answered)
	}))
	defer srv.Close()
	a, path := joinApp(t)
	providers := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(providers, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "tts-1", Voice: "alba"}

	const said = "the kettle is on"
	r, err := a.mint(dashboard.SpeakText{Text: said})
	if err != nil {
		t.Fatal(err)
	}
	a.speakStart(r)
	select {
	case <-asked: // the synthesis has its context and synthesizer, and is asking
	case <-time.After(5 * time.Second):
		t.Fatal("the synthesis never asked the service")
	}
	r.mu.Lock()
	locked := true
	unlock := func() {
		if locked {
			locked = false
			r.mu.Unlock()
		}
	}
	defer unlock()
	close(answer)
	<-answered
	holdWait(t, "the synthesis taking the service's answer", func() bool {
		var buf bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
		return strings.Contains(buf.String(), "(*spokenReply).add")
	})

	shutdownHeldBy(t, a, "the reply's synthesis", unlock)

	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	used, err := st.SpeechUsed(a.speechMonth(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 1 || used[0].Requests != 1 || used[0].Characters != len(said) {
		t.Fatalf("the piece the service answered was not metered before the store closed: %+v", used)
	}
}

// A REPLY STARTED ONCE THE IDENTITY IS STOPPING IS NOT SPOKEN, AND SAYS
// SO. No synthesis starts behind Shutdown: the reply ends with the
// stopping refusal, nothing is asked of the service, and its hold on the
// month goes back.
func TestAReplyStartedWhileStoppingIsRefusedAndNotSpoken(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(samples(240))
	}))
	defer srv.Close()
	a, _ := joinApp(t)
	providers := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(providers, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "tts-1", Voice: "alba"}
	r, err := a.mint(dashboard.SpeakText{Text: "the kettle is on"})
	if err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)
	a.speakStart(r)
	r.mu.Lock()
	done, rerr, reserved := r.done, r.err, r.reserved
	r.mu.Unlock()
	if !done || !errors.Is(rerr, errStopping) {
		t.Fatalf("a reply started behind Shutdown did not end with the stopping refusal: done=%v err=%v", done, rerr)
	}
	if reserved != 0 {
		t.Fatalf("the refused reply kept %d characters of the month", reserved)
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("the service was asked %d time(s) behind Shutdown", n)
	}
}
