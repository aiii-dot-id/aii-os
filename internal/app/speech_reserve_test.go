package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
func TestTheCeilingCountsWhatIsStillOnItsWay(t *testing.T) {
	arrived, release := make(chan struct{}, 8), make(chan struct{})
	var once sync.Once
	let := func() { once.Do(func() { close(release) }) }
	defer let()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		_, _ = io.ReadAll(r.Body)
		arrived <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(samples(40))
	}))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"`+srv.URL+`","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "m", Voice: "v", MonthlyCharacters: 10}
	ceiling := func(err error) bool { return err != nil && strings.Contains(err.Error(), "ceiling for spoken replies") }

	first, err := a.speakMint(dashboard.SpeakText{Text: "12345678"})
	if err != nil {
		t.Fatal(err)
	}
	played := make(chan error, 1)
	go func() { _, err := a.spokenAudio(context.Background(), first); played <- err }()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the first reply never reached the service")
	}
	// .
	// .
	if _, err := a.speakMint(dashboard.SpeakText{Text: "abcdefgh"}); !ceiling(err) || !strings.Contains(err.Error(), "on their way") {
		t.Fatalf("a second reply was admitted past the ceiling while the first was on its way: %v", err)
	}
	if asked.Load() != 1 {
		t.Fatalf("a refused reply reached the service: %d requests", asked.Load())
	}
	// .
	second, err := a.speakMint(dashboard.SpeakText{Text: "ab"})
	if err != nil {
		t.Fatalf("a reply that fits beside what is on its way was refused: %v", err)
	}
	let()
	if err := <-played; err != nil {
		t.Fatal(err)
	}
	if spent, _ := a.speechSpent("tts"); spent != 8 {
		t.Fatalf("the first reply was not metered as spoken: %d", spent)
	}
	// .
	// .
	if _, err := a.speakMint(dashboard.SpeakText{Text: "abc"}); !ceiling(err) {
		t.Fatalf("what is held and what is metered were not both counted: %v", err)
	}
	// .
	a.spokenMu.Lock()
	a.spoken[second].born = time.Now().Add(-sayFresh - time.Minute)
	a.sweepSpoken()
	a.spokenMu.Unlock()
	third, err := a.speakMint(dashboard.SpeakText{Text: "xy"})
	if err != nil {
		t.Fatalf("a swept reply kept its hold: %v", err)
	}
	if _, err := a.spokenAudio(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	if spent, _ := a.speechSpent("tts"); spent != 10 {
		t.Fatalf("the month's spend is not exact: %d", spent)
	}
	if _, err := a.speakMint(dashboard.SpeakText{Text: "z"}); !ceiling(err) {
		t.Fatalf("the ceiling is not exact once everything is recorded: %v", err)
	}
}

// .
// .
func TestListeningStillOnItsWayCountsAgainstTheCeiling(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.STT.MonthlyMinutes = 1
	if err := a.reserveHearing(40 * time.Second); err != nil {
		t.Fatal(err)
	}
	err := a.reserveHearing(30 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "ceiling for the microphone") || !strings.Contains(err.Error(), "on its way") {
		t.Fatalf("what is on its way was not counted: %v", err)
	}
	a.settleHearing(40 * time.Second)
	if err := a.reserveHearing(30 * time.Second); err != nil {
		t.Fatalf("a settled hold was still counted: %v", err)
	}
}
